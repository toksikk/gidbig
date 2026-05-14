package coffee

import (
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

type capturedBrewReadyCall struct {
	guildID   string
	channelID string
}

func captureBrewReadyMessages(m *Module, t *testing.T) func() []capturedBrewReadyCall {
	t.Helper()
	previous := m.sendBrewReadyMessage
	calls := []capturedBrewReadyCall{}
	m.sendBrewReadyMessage = func(_ *discordgo.Session, guildID, channelID string) {
		calls = append(calls, capturedBrewReadyCall{guildID: guildID, channelID: channelID})
	}
	t.Cleanup(func() { m.sendBrewReadyMessage = previous })
	return func() []capturedBrewReadyCall { return calls }
}

func useFixedCupSize(m *Module, t *testing.T, size float64) {
	t.Helper()
	previous := m.randCupSize
	m.randCupSize = func() float64 { return size }
	t.Cleanup(func() { m.randCupSize = previous })
}

func resetBrewStates(m *Module, t *testing.T) {
	t.Helper()
	m.brewMu.Lock()
	m.brewStates = map[string]*brewState{}
	m.brewMu.Unlock()
	t.Cleanup(func() {
		m.brewMu.Lock()
		m.brewStates = map[string]*brewState{}
		m.brewMu.Unlock()
	})
}

func setBrewReady(m *Module, guildID, channelID string) {
	m.brewMu.Lock()
	defer m.brewMu.Unlock()
	key := brewStateKey(guildID, channelID)
	if st, ok := m.brewStates[key]; ok {
		st.isReady = true
	}
}

func TestStartBrew_NewBrew(t *testing.T) {
	m := newTestModule(t)
	resetBrewStates(m, t)
	useNow(m, t, time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC))

	alreadyBrewing, readyAt := m.startBrew(nil, "guild1", "channel1")

	if alreadyBrewing {
		t.Fatal("expected new brew, got alreadyBrewing=true")
	}
	expected := time.Date(2026, 5, 4, 10, 3, 0, 0, time.UTC)
	if !readyAt.Equal(expected) {
		t.Errorf("readyAt = %v, want %v", readyAt, expected)
	}

	m.brewMu.Lock()
	st := m.brewStates[brewStateKey("guild1", "channel1")]
	m.brewMu.Unlock()

	if st == nil {
		t.Fatal("expected brew state to be created")
	}
	if st.isReady {
		t.Error("expected brew to not be ready yet")
	}
	if st.coffeeLiters != potCapacity {
		t.Errorf("coffeeLiters = %v, want %v", st.coffeeLiters, potCapacity)
	}
}

func TestStartBrew_AlreadyBrewing(t *testing.T) {
	m := newTestModule(t)
	resetBrewStates(m, t)
	useNow(m, t, time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC))

	m.startBrew(nil, "guild1", "channel1")
	alreadyBrewing, readyAt := m.startBrew(nil, "guild1", "channel1")

	if !alreadyBrewing {
		t.Fatal("expected alreadyBrewing=true for second /brew")
	}
	expected := time.Date(2026, 5, 4, 10, 3, 0, 0, time.UTC)
	if !readyAt.Equal(expected) {
		t.Errorf("readyAt = %v, want %v", readyAt, expected)
	}
}

func TestStartBrew_AllowsNewBrewAfterReady(t *testing.T) {
	m := newTestModule(t)
	resetBrewStates(m, t)
	useNow(m, t, time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC))

	m.startBrew(nil, "guild1", "channel1")
	setBrewReady(m, "guild1", "channel1")

	alreadyBrewing, _ := m.startBrew(nil, "guild1", "channel1")
	if alreadyBrewing {
		t.Error("expected to allow new brew after previous brew is ready")
	}
}

func TestMarkBrewReady_SendsMessage(t *testing.T) {
	m := newTestModule(t)
	resetBrewStates(m, t)
	getReadyCalls := captureBrewReadyMessages(m, t)

	m.brewMu.Lock()
	m.brewStates["guild1:channel1"] = &brewState{
		readyAt:      time.Now(),
		isReady:      false,
		coffeeLiters: potCapacity,
	}
	m.brewMu.Unlock()

	m.markBrewReady(nil, "guild1", "channel1")

	m.brewMu.Lock()
	st := m.brewStates["guild1:channel1"]
	m.brewMu.Unlock()

	if st == nil || !st.isReady {
		t.Error("expected brew to be marked ready")
	}

	calls := getReadyCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 ready message, got %d", len(calls))
	}
	if calls[0].guildID != "guild1" || calls[0].channelID != "channel1" {
		t.Errorf("ready call = %+v, want guild1/channel1", calls[0])
	}
}

func TestMarkBrewReady_NoStateDoesNothing(t *testing.T) {
	m := newTestModule(t)
	resetBrewStates(m, t)
	getReadyCalls := captureBrewReadyMessages(m, t)

	m.markBrewReady(nil, "guild1", "channel1")

	if len(getReadyCalls()) != 0 {
		t.Error("expected no ready message when no brew state exists")
	}
}

func TestBrewStateKey_WithGuild(t *testing.T) {
	key := brewStateKey("guild1", "channel1")
	if key != "guild1:channel1" {
		t.Errorf("key = %q, want guild1:channel1", key)
	}
}

func TestBrewStateKey_WithoutGuild(t *testing.T) {
	key := brewStateKey("", "channel1")
	if key != "dm:channel1" {
		t.Errorf("key = %q, want dm:channel1", key)
	}
}

func TestHasActiveBrew_NoState(t *testing.T) {
	m := newTestModule(t)
	resetBrewStates(m, t)
	if m.hasActiveBrew("guild1", "channel1") {
		t.Error("expected false with no brew state")
	}
}

func TestHasActiveBrew_WithState(t *testing.T) {
	m := newTestModule(t)
	resetBrewStates(m, t)
	m.brewMu.Lock()
	m.brewStates["guild1:channel1"] = &brewState{coffeeLiters: potCapacity}
	m.brewMu.Unlock()

	if !m.hasActiveBrew("guild1", "channel1") {
		t.Error("expected true with active brew state")
	}
}

func TestGrabCoffee_NotReady(t *testing.T) {
	m := newTestModule(t)
	resetBrewStates(m, t)
	result := m.grabCoffee("guild1", "channel1", "user1")
	if !result.notReady {
		t.Error("expected notReady=true with no brew state")
	}
}

func TestGrabCoffee_RandomCupSize(t *testing.T) {
	m := newTestModule(t)
	resetBrewStates(m, t)
	m.brewMu.Lock()
	m.brewStates["guild1:channel1"] = &brewState{
		isReady:      true,
		coffeeLiters: potCapacity,
	}
	m.brewMu.Unlock()

	result := m.grabCoffee("guild1", "channel1", "user1")
	if result.notReady {
		t.Fatal("expected successful grab")
	}
	if result.cupLiters < 0.15 || result.cupLiters > 0.35 {
		t.Errorf("cupLiters = %.3f, want between 0.150 and 0.350", result.cupLiters)
	}
	ml := int(result.cupLiters * 1000)
	if ml%10 != 0 {
		t.Errorf("cupLiters should be in 10ml increments, got %dml", ml)
	}
}

func TestGrabCoffee_MultipleCupsAllowed(t *testing.T) {
	m := newTestModule(t)
	resetBrewStates(m, t)
	useFixedCupSize(m, t, 0.25)
	m.brewMu.Lock()
	m.brewStates["guild1:channel1"] = &brewState{
		isReady:      true,
		coffeeLiters: potCapacity,
	}
	m.brewMu.Unlock()

	r1 := m.grabCoffee("guild1", "channel1", "user1")
	if r1.notReady {
		t.Fatal("first grab should succeed")
	}
	r2 := m.grabCoffee("guild1", "channel1", "user1")
	if r2.notReady {
		t.Fatal("second grab by same user should also succeed (no alreadyTaken restriction)")
	}

	m.brewMu.Lock()
	st := m.brewStates["guild1:channel1"]
	m.brewMu.Unlock()

	if st == nil {
		t.Fatal("expected brew state to still exist after two 0.25L grabs from 2L")
	}
	if len(st.grabs) != 2 {
		t.Errorf("expected 2 grab records, got %d", len(st.grabs))
	}
	want := potCapacity - 0.25 - 0.25
	if st.coffeeLiters != want {
		t.Errorf("coffeeLiters = %.2f, want %.2f", st.coffeeLiters, want)
	}
}

func TestGrabCoffee_AlwaysPlain(t *testing.T) {
	m := newTestModule(t)
	resetBrewStates(m, t)
	m.brewMu.Lock()
	m.brewStates["guild1:channel1"] = &brewState{
		isReady:      true,
		coffeeLiters: potCapacity,
	}
	m.brewMu.Unlock()

	m.grabCoffee("guild1", "channel1", "user1")

	m.brewMu.Lock()
	st := m.brewStates["guild1:channel1"]
	m.brewMu.Unlock()

	if st.grabs[0].cup.milk || st.grabs[0].cup.sugar {
		t.Error("grabCoffee should always produce a plain cup; milk/sugar are added separately")
	}
}

func TestGrabCoffee_PotEmpties(t *testing.T) {
	m := newTestModule(t)
	resetBrewStates(m, t)
	useFixedCupSize(m, t, 0.25)
	m.brewMu.Lock()
	m.brewStates["guild1:channel1"] = &brewState{
		isReady:      true,
		coffeeLiters: 0.25,
	}
	m.brewMu.Unlock()

	result := m.grabCoffee("guild1", "channel1", "user1")
	if result.notReady {
		t.Fatal("expected successful grab")
	}
	if !result.isEmpty {
		t.Error("expected isEmpty=true after draining the pot")
	}

	m.brewMu.Lock()
	_, exists := m.brewStates["guild1:channel1"]
	m.brewMu.Unlock()
	if exists {
		t.Error("expected brew state deleted after pot is empty")
	}
}

func TestGrabCoffee_CupsCappedToRemainingLiters(t *testing.T) {
	m := newTestModule(t)
	resetBrewStates(m, t)
	useFixedCupSize(m, t, 0.30)
	m.brewMu.Lock()
	m.brewStates["guild1:channel1"] = &brewState{
		isReady:      true,
		coffeeLiters: 0.05,
	}
	m.brewMu.Unlock()

	result := m.grabCoffee("guild1", "channel1", "user1")
	if result.notReady {
		t.Fatal("expected successful grab")
	}
	if result.cupLiters > 0.05+1e-9 {
		t.Errorf("cupLiters = %.4f, want <= 0.05 (capped to remaining)", result.cupLiters)
	}
	if !result.isEmpty {
		t.Error("expected isEmpty=true after draining last drop")
	}

	m.brewMu.Lock()
	_, exists := m.brewStates["guild1:channel1"]
	m.brewMu.Unlock()
	if exists {
		t.Error("expected brew state deleted after pot is empty")
	}
}

func TestAddToLastCup_NoBrew(t *testing.T) {
	m := newTestModule(t)
	resetBrewStates(m, t)
	result := m.addToLastCup("guild1", "channel1", "user1", true, false)
	if !result.notReady {
		t.Error("expected notReady=true with no brew state")
	}
}

func TestAddToLastCup_NoCup(t *testing.T) {
	m := newTestModule(t)
	resetBrewStates(m, t)
	m.brewMu.Lock()
	m.brewStates["guild1:channel1"] = &brewState{
		isReady:      true,
		coffeeLiters: potCapacity,
	}
	m.brewMu.Unlock()

	result := m.addToLastCup("guild1", "channel1", "user1", true, false)
	if !result.noCup {
		t.Error("expected noCup=true when user has not grabbed a cup")
	}
}

func TestAddToLastCup_AddsMilk(t *testing.T) {
	m := newTestModule(t)
	resetBrewStates(m, t)
	m.brewMu.Lock()
	m.brewStates["guild1:channel1"] = &brewState{
		isReady:      true,
		coffeeLiters: potCapacity,
		grabs:        []grabRecord{{userID: "user1", cup: CupTaken{liters: 0.25}}},
	}
	m.brewMu.Unlock()

	result := m.addToLastCup("guild1", "channel1", "user1", true, false)
	if result.noCup || result.notReady {
		t.Fatal("expected successful modification")
	}

	m.brewMu.Lock()
	cup := m.brewStates["guild1:channel1"].grabs[0].cup
	m.brewMu.Unlock()

	if !cup.milk {
		t.Error("expected milk=true after addToLastCup with milk")
	}
	if cup.sugar {
		t.Error("expected sugar=false (untouched)")
	}
}

func TestAddToLastCup_AddsSugar(t *testing.T) {
	m := newTestModule(t)
	resetBrewStates(m, t)
	m.brewMu.Lock()
	m.brewStates["guild1:channel1"] = &brewState{
		isReady:      true,
		coffeeLiters: potCapacity,
		grabs:        []grabRecord{{userID: "user1", cup: CupTaken{liters: 0.25}}},
	}
	m.brewMu.Unlock()

	m.addToLastCup("guild1", "channel1", "user1", false, true)

	m.brewMu.Lock()
	cup := m.brewStates["guild1:channel1"].grabs[0].cup
	m.brewMu.Unlock()

	if !cup.sugar {
		t.Error("expected sugar=true after addToLastCup with sugar")
	}
}

func TestAddToLastCup_CombinesMilkAndSugar(t *testing.T) {
	m := newTestModule(t)
	resetBrewStates(m, t)
	m.brewMu.Lock()
	m.brewStates["guild1:channel1"] = &brewState{
		isReady:      true,
		coffeeLiters: potCapacity,
		grabs:        []grabRecord{{userID: "user1", cup: CupTaken{liters: 0.25}}},
	}
	m.brewMu.Unlock()

	m.addToLastCup("guild1", "channel1", "user1", true, false)
	m.addToLastCup("guild1", "channel1", "user1", false, true)

	m.brewMu.Lock()
	cup := m.brewStates["guild1:channel1"].grabs[0].cup
	m.brewMu.Unlock()

	if !cup.milk || !cup.sugar {
		t.Errorf("expected milk=true sugar=true after both modifications, got milk=%v sugar=%v", cup.milk, cup.sugar)
	}
}

func TestAddToLastCup_ModifiesLastCupOnly(t *testing.T) {
	m := newTestModule(t)
	resetBrewStates(m, t)
	m.brewMu.Lock()
	m.brewStates["guild1:channel1"] = &brewState{
		isReady:      true,
		coffeeLiters: potCapacity,
		grabs: []grabRecord{
			{userID: "user1", cup: CupTaken{liters: 0.25}},
			{userID: "user1", cup: CupTaken{liters: 0.20}},
		},
	}
	m.brewMu.Unlock()

	m.addToLastCup("guild1", "channel1", "user1", true, false)

	m.brewMu.Lock()
	grabs := m.brewStates["guild1:channel1"].grabs
	m.brewMu.Unlock()

	if grabs[0].cup.milk {
		t.Error("first cup should not be modified")
	}
	if !grabs[1].cup.milk {
		t.Error("second (last) cup should have milk=true")
	}
}

func TestAddToLastCup_DoesNotAffectPotLevel(t *testing.T) {
	m := newTestModule(t)
	resetBrewStates(m, t)
	useFixedCupSize(m, t, 0.25)
	m.brewMu.Lock()
	m.brewStates["guild1:channel1"] = &brewState{
		isReady:      true,
		coffeeLiters: potCapacity,
		grabs:        []grabRecord{{userID: "user1", cup: CupTaken{liters: 0.25}}},
	}
	m.brewMu.Unlock()

	m.addToLastCup("guild1", "channel1", "user1", true, false)

	m.brewMu.Lock()
	liters := m.brewStates["guild1:channel1"].coffeeLiters
	m.brewMu.Unlock()

	if liters != potCapacity {
		t.Errorf("addToLastCup should not change pot level, got %.2fL", liters)
	}
}

func TestDefaultRandCupSize_Range(t *testing.T) {
	for range 1000 {
		size := defaultRandCupSize()
		if size < 0.15 || size > 0.35 {
			t.Errorf("cup size %v out of [0.15, 0.35] range", size)
		}
		ml := int(size * 1000)
		if ml%10 != 0 {
			t.Errorf("cup size %v not a multiple of 10ml", size)
		}
	}
}

func TestBuildBrewMessage_NoCups(t *testing.T) {
	st := &brewState{
		readyAnnouncement: "☕ Coffee is ready!",
		coffeeLiters:      2.0,
	}
	got := buildBrewMessage(st)
	if !strings.HasPrefix(got, "☕ Coffee is ready!") {
		t.Errorf("expected header in message, got %q", got)
	}
	if !strings.Contains(got, "2.00L remaining") {
		t.Errorf("expected remaining L in message, got %q", got)
	}
}

func TestBuildBrewMessage_WithCups(t *testing.T) {
	st := &brewState{
		readyAnnouncement: "Coffee is ready!",
		coffeeLiters:      1.25,
		grabs: []grabRecord{
			{userID: "alice", cup: CupTaken{liters: 0.25, milk: false, sugar: false}},
			{userID: "bob", cup: CupTaken{liters: 0.25, milk: true, sugar: false}},
			{userID: "alice", cup: CupTaken{liters: 0.25, milk: false, sugar: true}},
		},
	}
	got := buildBrewMessage(st)

	if !strings.Contains(got, "<@alice>: 2 cups") {
		t.Errorf("expected alice with 2 cups, got %q", got)
	}
	if !strings.Contains(got, "<@bob>: 1 cup") {
		t.Errorf("expected bob with 1 cup, got %q", got)
	}
	if !strings.Contains(got, "🥛") {
		t.Errorf("expected milk emoji for bob, got %q", got)
	}
	if !strings.Contains(got, "🍬") {
		t.Errorf("expected sugar emoji for alice's second cup, got %q", got)
	}
	if !strings.Contains(got, "1.25L remaining") {
		t.Errorf("expected remaining liters, got %q", got)
	}
}

func TestBuildBrewMessage_EmptyPot(t *testing.T) {
	st := &brewState{
		readyAnnouncement: "Coffee is ready!",
		coffeeLiters:      0.0,
		grabs: []grabRecord{
			{userID: "user1", cup: CupTaken{liters: 0.25}},
		},
	}
	got := buildBrewMessage(st)
	if !strings.Contains(got, "_(pot is empty)_") {
		t.Errorf("expected empty pot notice, got %q", got)
	}
}

func TestBuildBrewMessage_FallbackHeader(t *testing.T) {
	st := &brewState{
		coffeeLiters: 1.5,
	}
	got := buildBrewMessage(st)
	if !strings.HasPrefix(got, "☕ Coffee is ready! Grab your cup!") {
		t.Errorf("expected fallback header, got %q", got)
	}
}
