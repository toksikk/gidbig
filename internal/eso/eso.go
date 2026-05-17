package eso

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/llm"
	"github.com/toksikk/gidbig/internal/util"
)

const systemPromptTemplate = `Du bist ein esoterischer Unsinn-Generator. Produziere genau einen Satz mystischen, pseudo-tiefgründigen deutschen Unsinns. Falls ein Thema angegeben wird, beziehe dich darauf. Orientiere dich an Ton und Struktur dieser Beispiele:
{{examples}}
Antworte ausschließlich mit dem generierten Satz auf Deutsch. Keine Erklärung, keine Begrüßung.`

// Module implements bot.Module for the eso conspiracy-text plugin.
type Module struct {
	session   *discordgo.Session
	responder *util.AIResponder
}

// New returns a new eso Module.
func New() *Module { return &Module{} }

func (m *Module) Name() string { return "eso" }

func (m *Module) Init(d bot.Deps) error {
	m.session = d.Session

	pool := make([]string, 20)
	for i := range pool {
		pool[i] = buildMessage()
	}
	m.responder = &util.AIResponder{
		SystemPromptTemplate: systemPromptTemplate,
		ExamplePool:          pool,
		ExampleCount:         5,
		Fallback:             buildMessage,
		GenerateFn:           llm.GenerateMessage,
	}

	slog.Info("eso: initialized")
	return nil
}

func (m *Module) Commands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		{
			Name:        "eso",
			Description: "Erhalte esoterischen Unsinn",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "thema",
					Description: "Optionales Thema für den esoterischen Unsinn",
					Required:    false,
				},
			},
		},
	}
}

func (m *Module) Listeners() []bot.EventListener {
	return []bot.EventListener{m.onInteractionCreate}
}

func (m *Module) Components() []bot.ComponentHandler { return nil }
func (m *Module) Background() []bot.BackgroundTask   { return nil }
func (m *Module) Shutdown() error                    { return nil }

func (m *Module) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	if i.ApplicationCommandData().Name != "eso" {
		return
	}

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		slog.Error("eso: failed to defer interaction", "error", err)
		return
	}

	var subject string
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "thema" {
			subject = opt.StringValue()
			break
		}
	}

	go func() {
		userPrompt := "Generiere einen esoterischen Unsinn-Satz."
		if subject != "" {
			userPrompt = "Generiere esoterischen Unsinn über das Thema: " + subject
		}
		text := m.responder.GenerateWithPrompt(context.Background(), userPrompt)
		if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &text}); err != nil {
			slog.Error("eso: failed to edit interaction response", "error", err)
			return
		}
		if msg, err := s.InteractionResponse(i.Interaction); err == nil && msg != nil {
			util.ReactOnMessage(s, msg.ChannelID, msg.ID, "🧠", "add")
		}
	}()
}

func buildMessage() string {
	return fmt.Sprintf(
		"%s%s%s%s%s",
		ohai[rand.IntN(len(ohai))],
		buty[rand.IntN(len(buty))],
		wat[rand.IntN(len(wat))],
		dointings[rand.IntN(len(dointings))],
		todotings[rand.IntN(len(todotings))],
	)
}

var ohai = []string{
	"Unsere Idee ist, ",
	"Wir planen, ",
	"Der Erzengel Metatron hat uns auf die Idee gebracht, ",
	"Geleitet von jahrtausendealtem Erfahrungswissen kamen wir darauf, ",
	"Der Geist von Wilhelm Reich hat uns aufgetragen, ",
	"Weil wir die moderne Schulmedizin ablehnen, sehen wir es als unsere Pflicht, ",
	"Nach jahrelanger Meditation haben uns die Lichtwesen mit der Idee kontaktiert, ",
	"Weil wir dem Chemtrail-Terror entgegentreten wollen, planen wir, ",
	"Aufgrund unserer tiefen spirituellen Liebe zu unseren Mitmenschen kamen wir auf den Gedanken, ",
	"Unser Templerbruder vom alten Orden hat uns geraten, ",
	"Rudolf Steiners 4. Reinkarnation hat uns beauftragt, ",
	"Unsere Investoren vom Planeten Nibiru zwingen uns, ",
	"Weil wir uns gegen die Infrarot-Nazis zur Wehr setzen möchten, können wir nicht anders, als ",
	"Von den Erfolgen der Lichtheiler ausgehend ist es uns möglich, ",
	"Unsere Energiegruppe hat beschlossen, ",
	"Der Terror der Merkel-Junta lässt uns keine andere Wahl, als ",
	"Viktor Schaubergers Schriften haben uns dazu inspiriert, ",
	"Weil wir uns dazu berufen fühlen, in die Fußstapfen Nikola Teslas zu treten, haben wir vor, ",
	"Weil das jüdisch kontrollierte Finanzsystem uns keine andere Wahl lässt, ist es unsere Pflicht, ",
}

var buty = []string{
	"hoch energetisierte ",
	"tachyonisierte ",
	"deoxidisierte ",
	"mit Plutonium abgefüllte ",
	"homöopathisch potenzierte ",
	"mit Bachblütenessenzen angereicherte ",
	"mit Orgonenergie bestrahlte ",
	"nach der Silva-Methode behandelte ",
	"nach Plänen von Dr. Axel Stoll konstruierte ",
	"auf vedischer Wissenschaft basierende ",
	"mit Hilfe der Alpha-Synapsen-Programmierung korrigierte ",
	"pseudosymmetrische ",
	"gravimetrische ",
	"nicht-euklidische ",
	"nach Bauplänen der ODESSA konstruierte ",
	"biologisch-dynamisch erzeugte ",
	"radiästhetisch vibrierende ",
	"raum-zeitlich nach innen gerichtete ",
	"negativ bezinste ",
}

var wat = []string{
	"Abfalleimer ",
	"Akupunkturnadeln ",
	"Aschenbecher ",
	"Autoradios ",
	"Bilderrahmen ",
	"Bürolampen ",
	"Damenfahrräder ",
	"Dildos ",
	"Energiekarten ",
	"Energiekarten ",
	"Energiesparlampen ",
	"Erfrischungsgetränke ",
	"Fledermausköttel",
	"Globuli ",
	"Halbedelsteine ",
	"Kaffeemaschinen ",
	"Klangschalen",
	"Klaviere ",
	"Kleinwagen ",
	"Kopfkissen ",
	"Kupferleitungen ",
	"Käsebrötchen ",
	"Küchenmesser ",
	"Müsliriegel ",
	"Nickel-Cadmium-Batterien ",
	"Parkscheiben ",
	"Penispumpen ",
	"Postwurfsendungen ",
	"Rheumadecken ",
	"Tennissocken ",
	"Toilettenpapierrollen ",
	"Topflappen ",
	"USB-Kabel ",
	"Unterhemden ",
	"Voodoopuppen ",
	"Zahnimplantate ",
	"Zimmerpflanzen ",
	"goldbeschichtete Glasfaserenden ",
	"unidirektionale Kabel ",
	"Kopfhörer ",
}

var dointings = []string{
	"mit kosmischen Strahlungen zu bombardieren, ",
	"mit Mikrowellen und/oder Skalarwellen aufzuladen, ",
	"durch weiße Magie zum schwingen zu bringen, ",
	"in superionisiertes Wasser einzulegen, ",
	"durch alchemistische Rituale zu transformieren, ",
	"mit Urfrequenzen zu beschallen, ",
	"gemäß eines Implosionsstrudels anzuzapfen, ",
	"mit heiligem Radium 88 zu versetzen, ",
	"mit informiertem Wasser zu beträufeln, ",
	"von Indigo-Kindern bemalen zu lassen, ",
	"pentatonisch zu beschallen, ",
	"aurafotografisch zu erfassen, ",
	"pulsierenden Magnetfeldern auszusetzen, ",
	"mit Energieakkumulatoren zu modulieren, ",
	"harmonisch auszupendeln, ",
	"neurolinguistisch zu programmieren, ",
	"homöopathisch zu verschütteln, ",
}

var todotings = []string{
	"um den Benzinverbrauch von Kraftfahrzeugen zu reduzieren.",
	"um das Dirk-Hamer-Syndrom sogenannter Krebspatienten zu behandeln.",
	"um die Mind Control-Versuche der Illuminaten abzuwehren.",
	"um unseren Kunden zu mehr Energie und Lebensfreude zu verhelfen.",
	"um den Kontakt zu verstorbenen Angehörigen zu ermöglichen.",
	"um der Raffgier der Pharmaindustrie eine Alternative entgegen zu setzen.",
	"um den Menschen den Kontakt zu ihren früheren Inkarnationen zu erlauben.",
	"um Leidenden Teufel und Dämonen auszutreiben.",
	"um Impfungen gegen Tropenkrankheiten unnötig zu machen.",
	"um Big Pharma einen Strich durch die Rechnung zu machen.",
	"um die Bevölkerung zu einem gesünderen Leben zu führen.",
	"um den semitisch-reptiloiden Besatzern entgegen zu treten.",
	"um auch Laien Astralreisen zu ermöglichen.",
	"um den ungünstigen Tachyonenfluss in Altbauwohnungen zu korrigieren.",
	"um die Aura des Anwenders positiv zu beeinflussen.",
	"um die Wehen gebärender Frauen zu lindern.",
	"um unfruchtbaren Frauen den Kinderwunsch zu erfüllen.",
	"um die Selbstreinigungs- und Entschlackungsfähigkeit des Körpers anzuregen.",
	"um den Bilderbergern auf die Schliche zu kommen.",
	"um der Zinsknechtschaft ein Ende zu setzen.",
}
