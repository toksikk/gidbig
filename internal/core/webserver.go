package gidbig

import (
	"bufio"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"strconv"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
	"github.com/simplesurance/go-ip-anonymizer/ipanonymizer"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/eso"
	"github.com/toksikk/gidbig/internal/soundboard"
	"golang.org/x/oauth2"
)

const (
	header            string = "web/templates/header.html"
	footer            string = "web/templates/footer.html"
	templateDir       string = "web/templates/"
	sessionCookieName string = "gidbig-session"
)

type sessionData struct {
	State            string `json:"state,omitempty"`
	DiscordUserID    string `json:"discordUserID,omitempty"`
	DiscordUsername  string `json:"discordUsername,omitempty"`
	DiscordAvatarURL string `json:"discordAvatarURL,omitempty"`
	AccessToken      string `json:"accessToken,omitempty"`
}

type sessionStore struct {
	secret []byte
}

func newSessionStore(secret string) *sessionStore {
	key := sha256.Sum256([]byte(secret))
	return &sessionStore{secret: key[:]}
}

func (s *sessionStore) Get(r *http.Request) *sessionData {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return &sessionData{}
	}
	data, err := s.decrypt(cookie.Value)
	if err != nil {
		return &sessionData{}
	}
	return data
}

func (s *sessionStore) Save(w http.ResponseWriter, data *sessionData) error {
	encoded, err := s.encrypt(data)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    encoded,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (s *sessionStore) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *sessionStore) encrypt(data *sessionData) (string, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.secret)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, jsonData, nil)
	return base64.URLEncoding.EncodeToString(ciphertext), nil
}

func (s *sessionStore) decrypt(cookieValue string) (*sessionData, error) {
	ciphertext, err := base64.URLEncoding.DecodeString(cookieValue)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(s.secret)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("invalid ciphertext length")
	}
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	var data sessionData
	if err := json.Unmarshal(plaintext, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// soundboardAPI is the slice of the soundboard module the web UI uses.
type soundboardAPI interface {
	Enqueue(user *discordgo.User, guild *discordgo.Guild, command, soundname string) bool
	QueueStatus() []soundboard.QueueStatus
	Collections() []soundboard.Collection
}

// esoGenerator produces an esoteric sentence for the /api/eso endpoint.
type esoGenerator interface {
	GenerateText(context.Context, string) string
}

// oauthEndpoint is Discord's fixed OAuth2 endpoint pair.
var oauthEndpoint = oauth2.Endpoint{
	AuthURL:  "https://discordapp.com/api/oauth2/authorize",
	TokenURL: "https://discordapp.com/api/oauth2/token",
}

var ipAnonymizer = ipanonymizer.NewWithMask(
	net.CIDRMask(16, 32),
	net.CIDRMask(64, 128),
)

// webServer owns the optional HTTP UI. Its dependencies (the Discord session and
// the soundboard/eso modules it queries) are injected at construction instead
// of living in package-level state, so a test can build one without touching
// process-wide variables. The session store, templates and OAuth configuration
// are per-instance for the same reason.
type webServer struct {
	session    *discordgo.Session
	soundboard soundboardAPI
	eso        esoGenerator

	store      *sessionStore
	tmpls      map[string]*template.Template
	oauth      *oauth2.Config
	oauthState string
}

// newWebServer builds a web server around the injected session and modules. A
// nil module is stored as a nil interface so the handlers' nil checks (for
// example the /api/eso 503) still work for a nil *Module.
func newWebServer(config *cfg.Config, session *discordgo.Session, sb *soundboard.Module, esoMod *eso.Module) *webServer {
	ws := &webServer{
		session: session,
		store:   newSessionStore(config.Web.SessionSecret),
		tmpls:   map[string]*template.Template{},
		oauth: &oauth2.Config{
			ClientID:     config.Web.Oauth.ClientID,
			ClientSecret: config.Web.Oauth.ClientSecret,
			RedirectURL:  config.Web.Oauth.RedirectURI + "/discordCallback",
			Scopes:       []string{"identify", "guilds"},
			Endpoint:     oauthEndpoint,
		},
	}
	if sb != nil {
		ws.soundboard = sb
	}
	if esoMod != nil {
		ws.eso = esoMod
	}
	return ws
}

// loadTemplates parses the page templates into the instance's map.
func (ws *webServer) loadTemplates() {
	ws.tmpls["home.html"] = template.Must(template.ParseFiles(templateDir+"home.html", header, footer))
	ws.tmpls["internal.html"] = template.Must(template.ParseFiles(templateDir+"internal.html", header, footer))
	ws.tmpls["item.html"] = template.Must(template.ParseFiles(templateDir + "item.html"))
	ws.tmpls["itemrowstart.html"] = template.Must(template.ParseFiles(templateDir + "itemrowstart.html"))
	ws.tmpls["itemrowend.html"] = template.Must(template.ParseFiles(templateDir + "itemrowend.html"))
	ws.tmpls["collwrapstart.html"] = template.Must(template.ParseFiles(templateDir + "collwrapstart.html"))
	ws.tmpls["collwrapend.html"] = template.Must(template.ParseFiles(templateDir + "collwrapend.html"))
}

// startWebServer starts the HTTP UI around the injected session and modules.
func startWebServer(config *cfg.Config, session *discordgo.Session, sb *soundboard.Module, esoMod *eso.Module) {
	ws := newWebServer(config, session, sb, esoMod)
	ws.loadTemplates()

	mux := http.NewServeMux()
	mux.HandleFunc("/", ws.handleMain)
	mux.HandleFunc("/logout", ws.handleLogout)
	mux.HandleFunc("/discordLogin", ws.handleDiscordLogin)
	mux.HandleFunc("/discordCallback", ws.handleDiscordCallback)
	mux.HandleFunc("/playsound", ws.handlePlaySound)
	mux.HandleFunc("/api/queue", ws.handleAPIQueue)
	mux.HandleFunc("/api/eso", ws.handleAPIEso)
	mux.HandleFunc("/health", handleHealth)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	err := http.ListenAndServe(":"+strconv.Itoa(config.Web.Port), mux)
	if err != nil {
		slog.Error("could not start webserver", "error", err)
		os.Exit(1)
	}
}

func readSoundDescription(prefix, name string) (text, shortText string, ok bool) {
	file, err := os.Open(fmt.Sprintf("audio/%v_%v.txt", prefix, name))
	if err != nil {
		return "", "", false
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Scan()
	text = scanner.Text()
	shortText = text
	if len(text) > 20 {
		shortText = text[0:20] + "..."
	}
	return text, shortText, true
}

func (ws *webServer) handlePlaySound(w http.ResponseWriter, r *http.Request) {
	slog.Info("WebUI /playsound Request", "Requesting IP", r.RemoteAddr)
	err := r.ParseForm()
	if err != nil {
		slog.Error("could not ParseForm", "error", err)
		return
	}
	session := ws.store.Get(r)
	userID := session.DiscordUserID
	if userID == "" {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}
	var guild *discordgo.Guild
	var user *discordgo.User
	if ws.session != nil {
		user, _ = ws.session.User(userID)
		for _, g := range ws.session.State.Guilds {
			for _, vs := range g.VoiceStates {
				if vs.UserID == userID {
					guild = g
				}
			}
		}
	}
	if ws.soundboard != nil && ws.soundboard.Enqueue(user, guild, r.FormValue("command"), r.FormValue("soundname")) {
		http.Error(w, http.StatusText(200), 200)
	} else {
		http.Error(w, http.StatusText(500), 500)
	}
}

type guildQueueStatus struct {
	GuildID     string `json:"guild_id"`
	NowPlaying  string `json:"now_playing,omitempty"`
	QueueLength int    `json:"queue_length"`
}

func (ws *webServer) handleAPIQueue(w http.ResponseWriter, r *http.Request) {
	session := ws.store.Get(r)
	if session.DiscordUserID == "" {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}

	var guilds []guildQueueStatus
	if ws.soundboard != nil {
		for _, qs := range ws.soundboard.QueueStatus() {
			guilds = append(guilds, guildQueueStatus{
				GuildID:     qs.GuildID,
				NowPlaying:  qs.NowPlaying,
				QueueLength: qs.QueueLength,
			})
		}
	}

	if guilds == nil {
		guilds = []guildQueueStatus{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"guilds": guilds})
}

func (ws *webServer) handleAPIEso(w http.ResponseWriter, r *http.Request) {
	if ws.eso == nil {
		handleAPIEsoWithGenerator(w, r, nil)
		return
	}
	handleAPIEsoWithGenerator(w, r, ws.eso)
}

type esoRequest struct {
	Thema string `json:"thema"`
}

func handleAPIEsoWithGenerator(w http.ResponseWriter, r *http.Request, generator esoGenerator) {
	slog.Info("ESO API request", "method", r.Method, "path", r.URL.Path)

	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	if generator == nil {
		slog.Warn("ESO API unavailable: module not initialized")
		http.Error(w, "eso module not initialized", http.StatusServiceUnavailable)
		return
	}

	var request esoRequest
	if r.Method == http.MethodPost && r.Body != nil && r.ContentLength != 0 {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
			return
		}

		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			http.Error(w, "body must contain one JSON object", http.StatusBadRequest)
			return
		}
	}

	if utf8.RuneCountInString(request.Thema) > 200 {
		http.Error(w, "thema must not exceed 200 characters", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{
		"text": generator.GenerateText(r.Context(), request.Thema),
	}); err != nil {
		slog.Error("ESO API response encoding failed", "error", err)
	}
}

func (ws *webServer) handleMain(w http.ResponseWriter, r *http.Request) {
	logWebRequests(r)
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	session := ws.store.Get(r)
	if session.DiscordUsername != "" {
		username := session.DiscordUsername
		avatarURL := session.DiscordAvatarURL

		var prefixes []string
		var si []soundItem
		var collections []soundboard.Collection
		if ws.soundboard != nil {
			collections = ws.soundboard.Collections()
		}
		for _, sc := range collections {
			newSoundItemRandom := soundItem{
				Itemprefix:    sc.Prefix,
				Itemcommand:   "!" + sc.Prefix,
				Itemsoundname: "",
				Itemtext:      "random",
				Itemshorttext: "random",
			}
			prefixes = append(prefixes, sc.Prefix)
			si = append(si, newSoundItemRandom)
			for _, snd := range sc.Sounds {
				newSoundItem := soundItem{
					Itemprefix:    sc.Prefix,
					Itemcommand:   "!" + sc.Prefix,
					Itemsoundname: snd.Name,
					Itemtext:      "!" + sc.Prefix + " " + snd.Name,
					Itemshorttext: "!" + sc.Prefix + " " + snd.Name,
				}
				if text, shortText, ok := readSoundDescription(sc.Prefix, snd.Name); ok {
					newSoundItem.Itemtext = text
					newSoundItem.Itemshorttext = shortText
				}
				si = append(si, newSoundItem)
			}
		}

		td := templateData{
			Prefixes:  prefixes,
			Username:  username,
			AvatarURL: avatarURL,
		}

		err := ws.tmpls["internal.html"].ExecuteTemplate(w, "header", td)
		if err != nil {
			slog.Error("failed to execute template", "template", "internal.html/header", "error", err)
			return
		}

		currentPrefix := ""
		i := 0
		for _, snd := range si {
			if snd.Itemprefix != currentPrefix {
				if i != 0 {
					err = ws.tmpls["itemrowend.html"].Execute(w, nil)
					if err != nil {
						slog.Error("failed to execute template", "template", "itemrowend.html", "error", err)
						return
					}
					err = ws.tmpls["collwrapend.html"].Execute(w, nil)
					if err != nil {
						slog.Error("failed to execute template", "template", "collwrapend.html", "error", err)
						return
					}
					i = 0
				}
				err = ws.tmpls["collwrapstart.html"].Execute(w, snd)
				if err != nil {
					slog.Error("failed to execute template", "template", "collwrapstart.html", "error", err)
					return
				}
				currentPrefix = snd.Itemprefix
			}
			if i%4 == 0 {
				err = ws.tmpls["itemrowstart.html"].Execute(w, nil)
				if err != nil {
					slog.Error("failed to execute template", "template", "itemrowstart.html", "error", err)
					return
				}
			}
			err = ws.tmpls["item.html"].Execute(w, snd)
			if err != nil {
				slog.Error("failed to execute template", "template", "item.html", "error", err)
				return
			}
			if i%4 == 3 {
				err = ws.tmpls["itemrowend.html"].Execute(w, nil)
				if err != nil {
					slog.Error("failed to execute template", "template", "itemrowend.html", "error", err)
					return
				}
			}
			i++
		}

		// Explicitly close the last collection (loop only closes previous ones on prefix change)
		if i > 0 {
			err = ws.tmpls["collwrapend.html"].Execute(w, nil)
			if err != nil {
				slog.Error("failed to execute template", "template", "collwrapend.html", "error", err)
				return
			}
		}

		err = ws.tmpls["internal.html"].ExecuteTemplate(w, "footer", map[string]interface{}{})
		if err != nil {
			slog.Error("failed to execute template", "template", "internal.html/footer", "error", err)
			return
		}
		return
	}
	err := ws.tmpls["home.html"].ExecuteTemplate(w, "header", templateData{})
	if err != nil {
		slog.Error("unable to execute template", "error", err)
		return
	}
	err = ws.tmpls["home.html"].ExecuteTemplate(w, "footer", map[string]interface{}{})
	if err != nil {
		slog.Error("unable to execute template", "error", err)
		return
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(w, `{"status":"ok"}`); err != nil {
		slog.Error("health check write failed", "error", err)
	}
}

func (ws *webServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	slog.Info("WebUI /logout Request", "Requesting IP", r.RemoteAddr)
	ws.store.Clear(w)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (ws *webServer) handleDiscordLogin(w http.ResponseWriter, r *http.Request) {
	logWebRequests(r)
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		slog.Error("unable to Read", "error", err)
		return
	}
	ws.oauthState = base64.URLEncoding.EncodeToString(b)

	session := ws.store.Get(r)
	session.State = ws.oauthState
	err = ws.store.Save(w, session)
	if err != nil {
		slog.Error("unable to Save", "error", err)
	}

	url := ws.oauth.AuthCodeURL(ws.oauthState)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func (ws *webServer) handleDiscordCallback(w http.ResponseWriter, r *http.Request) {
	logWebRequests(r)
	session := ws.store.Get(r)
	if r.URL.Query().Get("state") != session.State {
		slog.Warn("oauth state mismatch; possible CSRF or cookies disabled")
		http.Error(w, "no state match; possible csrf OR cookies not enabled", http.StatusForbidden)
		return
	}

	state := r.FormValue("state")
	if state != ws.oauthState {
		slog.Warn("invalid oauth state", "expected", ws.oauthState, "got", state)
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return
	}

	token, err := ws.oauth.Exchange(context.Background(), r.FormValue("code"))
	if err != nil {
		slog.Error("oauth code exchange failed", "error", err)
		http.Error(w, "code exchange failed", http.StatusInternalServerError)
		return
	}

	if !token.Valid() {
		slog.Error("retrieved invalid oauth token")
		http.Error(w, "retrieved invalid token", http.StatusInternalServerError)
		return
	}

	dg, err := discordgo.New("Bearer " + token.AccessToken)
	if err != nil {
		slog.Error("failed to create discord session", "error", err)
		return
	}
	user, _ := dg.User("@me")

	avatarURL := ""
	if user.Avatar != "" {
		avatarURL = fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.png", user.ID, user.Avatar)
	}

	session.DiscordUserID = user.ID
	session.DiscordUsername = user.Username
	session.DiscordAvatarURL = avatarURL
	session.AccessToken = token.AccessToken
	err = ws.store.Save(w, session)
	if err != nil {
		slog.Error("unable to Save", "error", err)
		return
	}

	_ = dg.Close()

	http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
}

func parseIPPort(s string) (ip net.IP, port, space string, err error) {
	ip = net.ParseIP(s)
	if ip == nil {
		var host string
		host, port, err = net.SplitHostPort(s)
		if err != nil {
			return
		}
		if port != "" {
			// This check only makes sense if service names are not allowed
			if _, err = strconv.ParseUint(port, 10, 16); err != nil {
				return
			}
		}
		ip = net.ParseIP(host)
	}
	if ip == nil {
		err = errors.New("invalid address format")
	} else {
		space = "IPv6"
		if ip4 := ip.To4(); ip4 != nil {
			space = "IPv4"
			ip = ip4
		}
	}
	return
}

func logWebRequests(r *http.Request) {
	ip, port, _, err := parseIPPort(r.RemoteAddr)
	if err != nil {
		slog.Warn("Error parsing IP address for WebUI Request ", "Request URI", r.RequestURI)
		return
	}
	anonIP, err := ipAnonymizer.IPString(ip.String())
	if err != nil {
		slog.Warn("Could not anonymize IP address for WebUI Request ", "Request URI", r.RequestURI)
	} else {
		slog.Info("WebUI Request", "Request URI", r.RequestURI, "Requesting IP / Port", anonIP+port)
	}
}
