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
	"net"
	"net/http"
	"os"
	"strconv"

	"github.com/bwmarrin/discordgo"
	"github.com/simplesurance/go-ip-anonymizer/ipanonymizer"
	"github.com/toksikk/gidbig/internal/cfg"
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

var (
	discordOauthConfig = &oauth2.Config{
		RedirectURL:  "",
		ClientID:     "",
		ClientSecret: "",
		Scopes:       []string{"identify", "guilds"},
		Endpoint:     *endp,
	}
	// Some random string, random for each request
	oauthStateString string
	endp             = &oauth2.Endpoint{
		AuthURL:  "https://discordapp.com/api/oauth2/authorize",
		TokenURL: "https://discordapp.com/api/oauth2/token",
	}
	tmpls = map[string]*template.Template{}

	store *sessionStore

	ipAnonymizer = ipanonymizer.NewWithMask(
		net.CIDRMask(16, 32),
		net.CIDRMask(64, 128),
	)
)

// startWebServer
func startWebServer(config *cfg.Config) {
	tmpls["home.html"] = template.Must(template.ParseFiles(templateDir+"home.html", header, footer))
	tmpls["internal.html"] = template.Must(template.ParseFiles(templateDir+"internal.html", header, footer))
	tmpls["item.html"] = template.Must(template.ParseFiles(templateDir + "item.html"))
	tmpls["itemrowstart.html"] = template.Must(template.ParseFiles(templateDir + "itemrowstart.html"))
	tmpls["itemrowend.html"] = template.Must(template.ParseFiles(templateDir + "itemrowend.html"))
	tmpls["collwrapstart.html"] = template.Must(template.ParseFiles(templateDir + "collwrapstart.html"))
	tmpls["collwrapend.html"] = template.Must(template.ParseFiles(templateDir + "collwrapend.html"))

	store = newSessionStore(config.Web.SessionSecret)

	discordOauthConfig.ClientID = config.Web.Oauth.ClientID
	discordOauthConfig.ClientSecret = config.Web.Oauth.ClientSecret
	discordOauthConfig.RedirectURL = config.Web.Oauth.RedirectURI + "/discordCallback"

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleMain)
	mux.HandleFunc("/logout", handleLogout)
	mux.HandleFunc("/discordLogin", handleDiscordLogin)
	mux.HandleFunc("/discordCallback", handleDiscordCallback)
	mux.HandleFunc("/playsound", handlePlaySound)
	mux.HandleFunc("/api/queue", handleAPIQueue)
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

func handlePlaySound(w http.ResponseWriter, r *http.Request) {
	slog.Info("WebUI /playsound Request", "Requesting IP", r.RemoteAddr)
	err := r.ParseForm()
	if err != nil {
		slog.Error("could not ParseForm", "error", err)
		return
	}
	sound, soundCollection := findSoundAndCollection(r.FormValue("command"), r.FormValue("soundname"))
	session := store.Get(r)
	userID := session.DiscordUserID
	if userID == "" {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}
	var guild *discordgo.Guild
	user, _ := discord.User(userID)
	for _, g := range discord.State.Guilds {
		for _, vs := range g.VoiceStates {
			if vs.UserID == userID {
				guild = g
			}
		}
	}
	if user != nil && guild != nil && soundCollection != nil {
		if sound != nil {
			go enqueuePlay(user, guild, soundCollection, sound)
		} else {
			go enqueuePlay(user, guild, soundCollection, soundCollection.Random())
		}
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

func handleAPIQueue(w http.ResponseWriter, r *http.Request) {
	session := store.Get(r)
	if session.DiscordUserID == "" {
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}

	var guilds []guildQueueStatus
	mutex.Lock()
	for guildID, ch := range queues {
		gs := guildQueueStatus{
			GuildID:     guildID,
			QueueLength: len(ch),
		}
		if np, ok := nowPlaying[guildID]; ok && np != nil && np.Sound != nil {
			gs.NowPlaying = np.Sound.Name
		}
		guilds = append(guilds, gs)
	}
	mutex.Unlock()

	if guilds == nil {
		guilds = []guildQueueStatus{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"guilds": guilds})
}

func handleMain(w http.ResponseWriter, r *http.Request) {
	logWebRequests(r)
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	session := store.Get(r)
	if session.DiscordUsername != "" {
		username := session.DiscordUsername
		avatarURL := session.DiscordAvatarURL

		var prefixes []string
		var si []soundItem
		for _, sc := range COLLECTIONS {
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

		err := tmpls["internal.html"].ExecuteTemplate(w, "header", td)
		if err != nil {
			slog.Error("failed to execute template", "template", "internal.html/header", "error", err)
			return
		}

		currentPrefix := ""
		i := 0
		for _, snd := range si {
			if snd.Itemprefix != currentPrefix {
				if i != 0 {
					err = tmpls["itemrowend.html"].Execute(w, nil)
					if err != nil {
						slog.Error("failed to execute template", "template", "itemrowend.html", "error", err)
						return
					}
					err = tmpls["collwrapend.html"].Execute(w, nil)
					if err != nil {
						slog.Error("failed to execute template", "template", "collwrapend.html", "error", err)
						return
					}
					i = 0
				}
				err = tmpls["collwrapstart.html"].Execute(w, snd)
				if err != nil {
					slog.Error("failed to execute template", "template", "collwrapstart.html", "error", err)
					return
				}
				currentPrefix = snd.Itemprefix
			}
			if i%4 == 0 {
				err = tmpls["itemrowstart.html"].Execute(w, nil)
				if err != nil {
					slog.Error("failed to execute template", "template", "itemrowstart.html", "error", err)
					return
				}
			}
			err = tmpls["item.html"].Execute(w, snd)
			if err != nil {
				slog.Error("failed to execute template", "template", "item.html", "error", err)
				return
			}
			if i%4 == 3 {
				err = tmpls["itemrowend.html"].Execute(w, nil)
				if err != nil {
					slog.Error("failed to execute template", "template", "itemrowend.html", "error", err)
					return
				}
			}
			i++
		}

		// Explicitly close the last collection (loop only closes previous ones on prefix change)
		if i > 0 {
			err = tmpls["collwrapend.html"].Execute(w, nil)
			if err != nil {
				slog.Error("failed to execute template", "template", "collwrapend.html", "error", err)
				return
			}
		}

		err = tmpls["internal.html"].ExecuteTemplate(w, "footer", map[string]interface{}{})
		if err != nil {
			slog.Error("failed to execute template", "template", "internal.html/footer", "error", err)
			return
		}
		return
	}
	err := tmpls["home.html"].ExecuteTemplate(w, "header", templateData{})
	if err != nil {
		slog.Error("unable to execute template", "error", err)
		return
	}
	err = tmpls["home.html"].ExecuteTemplate(w, "footer", map[string]interface{}{})
	if err != nil {
		slog.Error("unable to execute template", "error", err)
		return
	}
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	slog.Info("WebUI /logout Request", "Requesting IP", r.RemoteAddr)
	store.Clear(w)
	http.Redirect(w, r, "/", http.StatusFound)
}

func handleDiscordLogin(w http.ResponseWriter, r *http.Request) {
	logWebRequests(r)
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		slog.Error("unable to Read", "error", err)
		return
	}
	oauthStateString = base64.URLEncoding.EncodeToString(b)

	session := store.Get(r)
	session.State = oauthStateString
	err = store.Save(w, session)
	if err != nil {
		slog.Error("unable to Save", "error", err)
	}

	url := discordOauthConfig.AuthCodeURL(oauthStateString)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func handleDiscordCallback(w http.ResponseWriter, r *http.Request) {
	logWebRequests(r)
	session := store.Get(r)
	if r.URL.Query().Get("state") != session.State {
		slog.Warn("oauth state mismatch; possible CSRF or cookies disabled")
		http.Error(w, "no state match; possible csrf OR cookies not enabled", http.StatusForbidden)
		return
	}

	state := r.FormValue("state")
	if state != oauthStateString {
		slog.Warn("invalid oauth state", "expected", oauthStateString, "got", state)
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return
	}

	token, err := discordOauthConfig.Exchange(context.Background(), r.FormValue("code"))
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
	err = store.Save(w, session)
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
