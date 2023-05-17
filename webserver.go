package gidbig

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"strconv"

	"github.com/bwmarrin/discordgo"
	"github.com/gorilla/mux"
	"github.com/gorilla/sessions"
	"github.com/simplesurance/go-ip-anonymizer/ipanonymizer"
	log "github.com/sirupsen/logrus"
	"github.com/toksikk/gidbig/pkg/cfg"
	"golang.org/x/oauth2"
)

const (
	header      string = "web/templates/header.html"
	footer      string = "web/templates/footer.html"
	templateDir string = "web/templates/"
)

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

	// TODO change secret
	store *sessions.CookieStore

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
	store = sessions.NewCookieStore([]byte(config.Cs))
	discordOauthConfig.ClientID = strconv.Itoa(config.Ci)
	discordOauthConfig.ClientSecret = config.Cs
	discordOauthConfig.RedirectURL = config.RedirectURL + "/discordCallback"

	r := mux.NewRouter()
	r.HandleFunc("/", handleMain)
	r.HandleFunc("/logout", handleLogout)
	r.HandleFunc("/discordLogin", handleDiscordLogin)
	r.HandleFunc("/discordCallback", handleDiscordCallback)
	r.HandleFunc("/playsound", handlePlaySound)
	http.Handle("/", r)
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
	err := http.ListenAndServe(":"+strconv.Itoa(config.Port), nil)
	if err != nil {
		log.Fatal("could not start webserver: ", err)
	}
}

func handlePlaySound(w http.ResponseWriter, r *http.Request) {
	log.Infoln("WebUI /playsound Request from " + r.RemoteAddr)
	err := r.ParseForm()
	if err != nil {
		log.Error("could not ParseForm: ", err)
	}
	sound, soundCollection := findSoundAndCollection(r.FormValue("command"), r.FormValue("soundname"))
	session, _ := store.Get(r, "gidbig-session")
	var guild *discordgo.Guild
	user, _ := discord.User(session.Values["discordUserID"].(string))
	for _, g := range discord.State.Guilds {
		for _, vs := range g.VoiceStates {
			if vs.UserID == session.Values["discordUserID"].(string) {
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

func handleMain(w http.ResponseWriter, r *http.Request) {
	logWebRequests(r)
	session, _ := store.Get(r, "gidbig-session")
	if session.Values["discordUsername"] != nil {
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
				file, e := os.Open(fmt.Sprintf("audio/%v_%v.txt", sc.Prefix, snd.Name))
				if e == nil {
					scanner := bufio.NewScanner(file)
					scanner.Scan()
					text := scanner.Text()
					newSoundItem.Itemtext = text
					if len(text) > 20 {
						text = text[0:20]
						text += "..."
					}
					newSoundItem.Itemshorttext = text
				}
				si = append(si, newSoundItem)
			}
		}
		err := tmpls["internal.html"].ExecuteTemplate(w, "header", prefixes)
		if err != nil {
			fmt.Println(err)
			return
		}

		var currentPrefix string = ""
		var i int = 0
		for _, snd := range si {
			if snd.Itemprefix != currentPrefix {
				if i != 0 {
					err = tmpls["itemrowend.html"].Execute(w, nil)
					if err != nil {
						fmt.Println(err)
						return
					}
					err = tmpls["collwrapend.html"].Execute(w, nil)
					if err != nil {
						fmt.Println(err)
						return
					}
					i = 0
				}
				err = tmpls["collwrapstart.html"].Execute(w, snd)
				if err != nil {
					fmt.Println(err)
					return
				}
				currentPrefix = snd.Itemprefix
			}
			if i%4 == 0 {
				err = tmpls["itemrowstart.html"].Execute(w, nil)
				if err != nil {
					fmt.Println(err)
					return
				}
			}
			err = tmpls["item.html"].Execute(w, snd)
			if err != nil {
				fmt.Println(err)
				return
			}
			if i%4 == 3 {
				err = tmpls["itemrowend.html"].Execute(w, nil)
				if err != nil {
					fmt.Println(err)
					return
				}
			}
			i++
		}

		err = tmpls["internal.html"].ExecuteTemplate(w, "footer", map[string]interface{}{})
		if err != nil {
			fmt.Println(err)
			return
		}
		return
	}
	err := tmpls["home.html"].ExecuteTemplate(w, "header", map[string]interface{}{})
	if err != nil {
		log.Error("unable to execute template: ", err)
	}
	err = tmpls["home.html"].ExecuteTemplate(w, "footer", map[string]interface{}{})
	if err != nil {
		log.Error("unable to execute template: ", err)
	}
}
func handleLogout(w http.ResponseWriter, r *http.Request) {
	log.Infoln("WebUI /logout Request from " + r.RemoteAddr)
	cookie := &http.Cookie{
		Name:   "gidbig-session",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	}
	http.SetCookie(w, cookie)
	http.Redirect(w, r, "/", http.StatusFound)
}
func handleDiscordLogin(w http.ResponseWriter, r *http.Request) {
	logWebRequests(r)
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		log.Error("unable to Read: ", err)
	}
	oauthStateString = base64.URLEncoding.EncodeToString(b)

	session, _ := store.Get(r, "gidbig-session")
	session.Values["state"] = oauthStateString
	err = session.Save(r, w)
	if err != nil {
		log.Error("unable to Save: ", err)
	}

	url := discordOauthConfig.AuthCodeURL(oauthStateString)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func handleDiscordCallback(w http.ResponseWriter, r *http.Request) {
	logWebRequests(r)
	session, err := store.Get(r, "gidbig-session")
	if err != nil {
		fmt.Fprintln(w, "aborted")
		return
	}
	if r.URL.Query().Get("state") != session.Values["state"] {
		fmt.Fprintln(w, "no state match; possible csrf OR cookies not enabled")
		return
	}

	state := r.FormValue("state")
	if state != oauthStateString {
		fmt.Printf("invalid oauth state, expected '%s', got '%s'\n", oauthStateString, state)
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return
	}

	token, err := discordOauthConfig.Exchange(context.Background(), r.FormValue("code"))
	if err != nil {
		fmt.Fprintln(w, "Code exchange (could not get token) failed with ", err)
		return
	}

	if !token.Valid() {
		fmt.Fprintln(w, "retrieved invalid token")
		return
	}

	dg, err := discordgo.New("Bearer " + token.AccessToken)
	if err != nil {
		fmt.Println("Error while creating discord session: ", err)
		return
	}
	user, _ := dg.User("@me")

	session.Values["discordUserID"] = user.ID
	session.Values["discordUsername"] = user.Username
	session.Values["accessToken"] = token.AccessToken
	err = session.Save(r, w)
	if err != nil {
		log.Error("unable to Save: ", err)
	}

	dg.Close()

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
		log.Warnln("Error parsing IP address for WebUI Request to " + r.RequestURI)
	}
	anonIP, err := ipAnonymizer.IPString(ip.String())
	if err != nil {
		log.Warnln("Could not anonymize IP address for WebUI Request to " + r.RequestURI)
	} else {
		log.Infoln("WebUI Request to " + r.RequestURI + " from " + anonIP + port)
	}
}
