package status

import (
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/pkg/util"
)

func Start(discord *discordgo.Session) {
	go setIdleStatus(discord)
}

func setIdleStatus(discord *discordgo.Session) {
	games := []string{
		"Terranigma",
		"Secret of Mana",
		"Quake III Arena",
		"Duke Nukem 3D",
		"Monkey Island 2: LeChuck's Revenge",
		"Turtles in Time",
		"Unreal Tournament",
		"Half-Life",
		"Half-Life 2",
		"Warcraft II",
		"Starcraft",
		"Diablo",
		"Diablo II",
		"A Link to the Past",
		"Ocarina of Time",
		"Star Fox",
		"Tetris",
		"Pokémon Red",
		"Pokémon Blue",
		"Die Siedler II",
		"Day of the Tentacle",
		"Maniac Mansion",
		"Prince of Persia",
		"Super Mario Kart",
		"Pac-Man",
		"Frogger",
		"Donkey Kong",
		"Donkey Kong Country",
		"Asteroids",
		"Doom",
		"Breakout",
		"Street Fighter II",
		"Wolfenstein 3D",
		"Mega Man",
		"Myst",
		"R-Type",
	}
	for {
		discord.UpdateStreamingStatus(1, "", "")
		discord.UpdateGameStatus(0, games[util.RandomRange(0, len(games))])
		time.Sleep(time.Duration(util.RandomRange(5, 15)) * time.Minute)
	}
}
