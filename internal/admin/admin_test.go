package admin

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestCommands_Structure(t *testing.T) {
	cmds := Commands()
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
	cmd := cmds[0]
	if cmd.Name != "admin" {
		t.Errorf("Name = %q, want %q", cmd.Name, "admin")
	}

	names := make(map[string]bool)
	for _, opt := range cmd.Options {
		names[opt.Name] = true
	}
	for _, want := range []string{"coffee", "gippity", "info"} {
		if !names[want] {
			t.Errorf("missing top-level option %q", want)
		}
	}
}

func TestCommands_CoffeeBeveragesSubcommand(t *testing.T) {
	cmds := Commands()
	cmd := cmds[0]
	var coffeeGroup *discordgo.ApplicationCommandOption
	for _, o := range cmd.Options {
		if o.Name == "coffee" {
			coffeeGroup = o
		}
	}
	if coffeeGroup == nil {
		t.Fatal("no coffee subcommand group")
	}
	if coffeeGroup.Type != discordgo.ApplicationCommandOptionSubCommandGroup {
		t.Errorf("coffee Type = %v, want SubCommandGroup", coffeeGroup.Type)
	}

	var beverages *discordgo.ApplicationCommandOption
	for _, o := range coffeeGroup.Options {
		if o.Name == "beverages" {
			beverages = o
		}
	}
	if beverages == nil {
		t.Fatal("no beverages subcommand under coffee")
	}
	if beverages.Type != discordgo.ApplicationCommandOptionSubCommand {
		t.Errorf("beverages Type = %v, want SubCommand", beverages.Type)
	}
}

func TestCommands_GippitySubcommands(t *testing.T) {
	cmds := Commands()
	cmd := cmds[0]
	var gippityGroup *discordgo.ApplicationCommandOption
	for _, o := range cmd.Options {
		if o.Name == "gippity" {
			gippityGroup = o
		}
	}
	if gippityGroup == nil {
		t.Fatal("no gippity subcommand group")
	}

	subNames := make(map[string]bool)
	for _, o := range gippityGroup.Options {
		subNames[o.Name] = true
	}
	for _, want := range []string{"privacy", "history"} {
		if !subNames[want] {
			t.Errorf("missing gippity subcommand %q", want)
		}
	}
}

func TestCommands_InfoSubcommand(t *testing.T) {
	cmds := Commands()
	cmd := cmds[0]
	var info *discordgo.ApplicationCommandOption
	for _, o := range cmd.Options {
		if o.Name == "info" {
			info = o
		}
	}
	if info == nil {
		t.Fatal("no info subcommand")
	}
	if info.Type != discordgo.ApplicationCommandOptionSubCommand {
		t.Errorf("info Type = %v, want SubCommand", info.Type)
	}
}

func TestCallerID_Member(t *testing.T) {
	i := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Member: &discordgo.Member{
				User: &discordgo.User{ID: "member-user-id"},
			},
		},
	}
	got := callerID(i)
	if got != "member-user-id" {
		t.Errorf("callerID = %q, want %q", got, "member-user-id")
	}
}

func TestCallerID_User(t *testing.T) {
	i := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			User: &discordgo.User{ID: "direct-user-id"},
		},
	}
	got := callerID(i)
	if got != "direct-user-id" {
		t.Errorf("callerID = %q, want %q", got, "direct-user-id")
	}
}

func TestCallerID_Neither(t *testing.T) {
	i := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{},
	}
	got := callerID(i)
	if got != "" {
		t.Errorf("callerID = %q, want empty string", got)
	}
}

func TestOptUserID_Present(t *testing.T) {
	user := &discordgo.User{ID: "target-user"}
	opts := []*discordgo.ApplicationCommandInteractionDataOption{
		{
			Name:  "user",
			Type:  discordgo.ApplicationCommandOptionUser,
			Value: user.ID,
		},
	}
	got := optUserID(nil, opts)
	if got != user.ID {
		t.Errorf("optUserID = %q, want %q", got, user.ID)
	}
}

func TestOptUserID_Absent(t *testing.T) {
	opts := []*discordgo.ApplicationCommandInteractionDataOption{}
	got := optUserID(nil, opts)
	if got != "" {
		t.Errorf("optUserID = %q, want empty string", got)
	}
}
