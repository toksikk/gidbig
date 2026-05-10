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

func captureBrewReadyMessages(t *testing.T) func() []capturedBrewReadyCall {
	t.Helper()
	previous := sendBrewReadyMessage
	calls := []capturedBrewReadyCall{}
	sendBrewReadyMessage = func(_ *discordgo.Session, guildID, channelID string) {
		calls = append(calls, capturedBrewReadyCall{guildID: guildID, channelID: channelID})
	}
	t.Cleanup(func() { sendBrewReadyMessage = previous })
	return func() []capturedBrewReadyCall { return calls }
}

func useFixedCupSize(t *testing.T, size float64) {
	t.Helper()
	previous := randCupSize
	randCupSize = func() float64 { return size }
	t.Cleanup(func() { randCupSize = previous })
}

func resetBrewStates(t *testing.T) {
	t.Helper()
	brewMu.Lock()
	brewStates = map[string]*brewState{}
	brewMu.Unlock()
	t.Cleanup(func() {
		brewMu.Lock()
		brewStates = map[string]*brewState{}
		brewMu.Unlock()
	})
}

func setBrewReady(guildID, channelID string) {
	brewMu.Lock()
	defer brewMu.Unlock()
	key := brewStateKey(guildID, channelID)
	if st, ok := brewStates[key]; ok {
		st.isReady = true
	}
}

func TestStartBrew_NewBrew(t *testing.T) {
	resetBrewStates(t)
	useNow(t, time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC))

	alreadyBrewing, readyAt := startBrew(nil, "guild1", "channel1")

	if alreadyBrewing {
		t.Fatal("expected new brew, got alreadyBrewing=true")
	}
	expected := time.Date(2026, 5, 4, 10, 3, 0, 0, time.UTC)
	if !readyAt.Equal(expected) {
		t.Errorf("readyAt = %v, want %v", readyAt, expected)
	}

	brewMu.Lock()
	st := brewStates[brewStateKey("guild1", "channel1")]
	brewMu.Unlock()

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
	resetBrewStates(t)
	useNow(t, time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC))

	startBrew(nil, "guild1", "channel1")
	alreadyBrewing, readyAt := startBrew(nil, "guild1", "channel1")

	if !alreadyBrewing {
		t.Fatal("expected alreadyBrewing=true for second /brew")
	}
	expected := time.Date(2026, 5, 4, 10, 3, 0, 0, time.UTC)
	if !readyAt.Equal(expected) {
		t.Errorf("readyAt = %v, want %v", readyAt, expected)
	}
}

func TestStartBrew_AllowsNewBrewAfterReady(t *testing.T) {
	resetBrewStates(t)
	useNow(t, time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC))

	startBrew(nil, "guild1", "channel1")
	setBrewReady("guild1", "channel1")

	alreadyBrewing, _ := startBrew(nil, "guild1", "channel1")
	if alreadyBrewing {
		t.Error("expected to allow new brew after previous brew is ready")
	}
}

func TestMarkBrewReady_SendsMessage(t *testing.T) {
	resetBrewStates(t)
	getReadyCalls := captureBrewReadyMessages(t)

	brewMu.Lock()
	brewStates["guild1:channel1"] = &brewState{
		readyAt:      time.Now(),
		isReady:      false,
		coffeeLiters: potCapacity,
	}
	brewMu.Unlock()

	markBrewReady(nil, "guild1", "channel1")

	brewMu.Lock()
	st := brewStates["guild1:channel1"]
	brewMu.Unlock()

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
	resetBrewStates(t)
	getReadyCalls := captureBrewReadyMessages(t)

	markBrewReady(nil, "guild1", "channel1")

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
	resetBrewStates(t)
	if hasActiveBrew("guild1", "channel1") {
		t.Error("expected false with no brew state")
	}
}

func TestHasActiveBrew_WithState(t *testing.T) {
	resetBrewStates(t)
	brewMu.Lock()
	brewStates["guild1:channel1"] = &brewState{coffeeLiters: potCapacity}
	brewMu.Unlock()

	if !hasActiveBrew("guild1", "channel1") {
		t.Error("expected true with active brew state")
	}
}

func TestGrabCoffee_NotReady(t *testing.T) {
	resetBrewStates(t)
	result := grabCoffee("guild1", "channel1", "user1")
	if !result.notReady {
		t.Error("expected notReady=true with no brew state")
	}
}

func TestGrabCoffee_RandomCupSize(t *testing.T) {
	resetBrewStates(t)
	brewMu.Lock()
	brewStates["guild1:channel1"] = &brewState{
		isReady:      true,
		coffeeLiters: potCapacity,
	}
	brewMu.Unlock()

	result := grabCoffee("guild1", "channel1", "user1")
	if result.notReady {
		t.Fatal("expected successful grab")
	}
	if result.cupML < 0.15 || result.cupML > 0.35 {
		t.Errorf("cupML = %.3f, want between 0.150 and 0.350", result.cupML)
	}
	ml := int(result.cupML * 1000)
	if ml%10 != 0 {
		t.Errorf("cupML should be in 10ml increments, got %dml", ml)
	}
}

func TestGrabCoffee_MultipleCupsAllowed(t *testing.T) {
	resetBrewStates(t)
	useFixedCupSize(t, 0.25)
	brewMu.Lock()
	brewStates["guild1:channel1"] = &brewState{
		isReady:      true,
		coffeeLiters: potCapacity,
	}
	brewMu.Unlock()

	r1 := grabCoffee("guild1", "channel1", "user1")
	if r1.notReady {
		t.Fatal("first grab should succeed")
	}
	r2 := grabCoffee("guild1", "channel1", "user1")
	if r2.notReady {
		t.Fatal("second grab by same user should also succeed (no alreadyTaken restriction)")
	}

	brewMu.Lock()
	st := brewStates["guild1:channel1"]
	brewMu.Unlock()

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
	resetBrewStates(t)
	brewMu.Lock()
	brewStates["guild1:channel1"] = &brewState{
		isReady:      true,
		coffeeLiters: potCapacity,
	}
	brewMu.Unlock()

	grabCoffee("guild1", "channel1", "user1")

	brewMu.Lock()
	st := brewStates["guild1:channel1"]
	brewMu.Unlock()

	if st.grabs[0].cup.milk || st.grabs[0].cup.sugar {
		t.Error("grabCoffee should always produce a plain cup; milk/sugar are added separately")
	}
}

func TestGrabCoffee_PotEmpties(t *testing.T) {
	resetBrewStates(t)
	useFixedCupSize(t, 0.25)
	brewMu.Lock()
	brewStates["guild1:channel1"] = &brewState{
		isReady:      true,
		coffeeLiters: 0.25,
	}
	brewMu.Unlock()

	result := grabCoffee("guild1", "channel1", "user1")
	if result.notReady {
		t.Fatal("expected successful grab")
	}
	if !result.isEmpty {
		t.Error("expected isEmpty=true after draining the pot")
	}

	brewMu.Lock()
	_, exists := brewStates["guild1:channel1"]
	brewMu.Unlock()
	if exists {
		t.Error("expected brew state deleted after pot is empty")
	}
}

func TestGrabCoffee_CupsCapToRemainingML(t *testing.T) {
	resetBrewStates(t)
	useFixedCupSize(t, 0.30) // cup wants 300ml but only 50ml left
	brewMu.Lock()
	brewStates["guild1:channel1"] = &brewState{
		isReady:      true,
		coffeeLiters: 0.05,
	}
	brewMu.Unlock()

	result := grabCoffee("guild1", "channel1", "user1")
	if result.notReady {
		t.Fatal("expected successful grab")
	}
	if result.cupML > 0.05+1e-9 {
		t.Errorf("cupML = %.4f, want <= 0.05 (capped to remaining)", result.cupML)
	}
	if !result.isEmpty {
		t.Error("expected isEmpty=true after draining last drop")
	}

	brewMu.Lock()
	_, exists := brewStates["guild1:channel1"]
	brewMu.Unlock()
	if exists {
		t.Error("expected brew state deleted after pot is empty")
	}
}

func TestAddToLastCup_NoBrew(t *testing.T) {
	resetBrewStates(t)
	result := addToLastCup("guild1", "channel1", "user1", true, false)
	if !result.notReady {
		t.Error("expected notReady=true with no brew state")
	}
}

func TestAddToLastCup_NoCup(t *testing.T) {
	resetBrewStates(t)
	brewMu.Lock()
	brewStates["guild1:channel1"] = &brewState{
		isReady:      true,
		coffeeLiters: potCapacity,
	}
	brewMu.Unlock()

	result := addToLastCup("guild1", "channel1", "user1", true, false)
	if !result.noCup {
		t.Error("expected noCup=true when user has not grabbed a cup")
	}
}

func TestAddToLastCup_AddsMilk(t *testing.T) {
	resetBrewStates(t)
	brewMu.Lock()
	brewStates["guild1:channel1"] = &brewState{
		isReady:      true,
		coffeeLiters: potCapacity,
		grabs:        []grabRecord{{userID: "user1", cup: CupTaken{ml: 0.25}}},
	}
	brewMu.Unlock()

	result := addToLastCup("guild1", "channel1", "user1", true, false)
	if result.noCup || result.notReady {
		t.Fatal("expected successful modification")
	}

	brewMu.Lock()
	cup := brewStates["guild1:channel1"].grabs[0].cup
	brewMu.Unlock()

	if !cup.milk {
		t.Error("expected milk=true after addToLastCup with milk")
	}
	if cup.sugar {
		t.Error("expected sugar=false (untouched)")
	}
}

func TestAddToLastCup_AddsSugar(t *testing.T) {
	resetBrewStates(t)
	brewMu.Lock()
	brewStates["guild1:channel1"] = &brewState{
		isReady:      true,
		coffeeLiters: potCapacity,
		grabs:        []grabRecord{{userID: "user1", cup: CupTaken{ml: 0.25}}},
	}
	brewMu.Unlock()

	addToLastCup("guild1", "channel1", "user1", false, true)

	brewMu.Lock()
	cup := brewStates["guild1:channel1"].grabs[0].cup
	brewMu.Unlock()

	if !cup.sugar {
		t.Error("expected sugar=true after addToLastCup with sugar")
	}
}

func TestAddToLastCup_CombinesMilkAndSugar(t *testing.T) {
	resetBrewStates(t)
	brewMu.Lock()
	brewStates["guild1:channel1"] = &brewState{
		isReady:      true,
		coffeeLiters: potCapacity,
		grabs:        []grabRecord{{userID: "user1", cup: CupTaken{ml: 0.25}}},
	}
	brewMu.Unlock()

	addToLastCup("guild1", "channel1", "user1", true, false)
	addToLastCup("guild1", "channel1", "user1", false, true)

	brewMu.Lock()
	cup := brewStates["guild1:channel1"].grabs[0].cup
	brewMu.Unlock()

	if !cup.milk || !cup.sugar {
		t.Errorf("expected milk=true sugar=true after both modifications, got milk=%v sugar=%v", cup.milk, cup.sugar)
	}
}

func TestAddToLastCup_ModifiesLastCupOnly(t *testing.T) {
	resetBrewStates(t)
	brewMu.Lock()
	brewStates["guild1:channel1"] = &brewState{
		isReady:      true,
		coffeeLiters: potCapacity,
		grabs: []grabRecord{
			{userID: "user1", cup: CupTaken{ml: 0.25}},
			{userID: "user1", cup: CupTaken{ml: 0.20}},
		},
	}
	brewMu.Unlock()

	addToLastCup("guild1", "channel1", "user1", true, false)

	brewMu.Lock()
	grabs := brewStates["guild1:channel1"].grabs
	brewMu.Unlock()

	if grabs[0].cup.milk {
		t.Error("first cup should not be modified")
	}
	if !grabs[1].cup.milk {
		t.Error("second (last) cup should have milk=true")
	}
}

func TestAddToLastCup_DoesNotAffectPotLevel(t *testing.T) {
	resetBrewStates(t)
	useFixedCupSize(t, 0.25)
	brewMu.Lock()
	brewStates["guild1:channel1"] = &brewState{
		isReady:      true,
		coffeeLiters: potCapacity,
		grabs:        []grabRecord{{userID: "user1", cup: CupTaken{ml: 0.25}}},
	}
	brewMu.Unlock()

	addToLastCup("guild1", "channel1", "user1", true, false)

	brewMu.Lock()
	liters := brewStates["guild1:channel1"].coffeeLiters
	brewMu.Unlock()

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
			{userID: "alice", cup: CupTaken{ml: 0.25, milk: false, sugar: false}},
			{userID: "bob", cup: CupTaken{ml: 0.25, milk: true, sugar: false}},
			{userID: "alice", cup: CupTaken{ml: 0.25, milk: false, sugar: true}},
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
			{userID: "user1", cup: CupTaken{ml: 0.25}},
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
