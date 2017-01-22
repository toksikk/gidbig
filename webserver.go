package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"os"

	"github.com/bwmarrin/discordgo"
	"github.com/gorilla/mux"
	"github.com/gorilla/sessions"
	"golang.org/x/oauth2"
)

const (
	header      = "templates/header.html"
	footer      = "templates/footer.html"
	templateDir = "templates/"
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
)

// SoundItem is used to represent a sound of our COLLECTIONS for html generation
type SoundItem struct {
	Itemprefix    string
	Itemcommand   string
	Itemsoundname string
	Itemtext      string
	Itemshorttext string
}

// startWebServer
func startWebServer(port string, ci string, cs string, redirectURL string) {
	tmpls["home.html"] = template.Must(template.ParseFiles(templateDir+"home.html", header, footer))
	tmpls["internal.html"] = template.Must(template.ParseFiles(templateDir+"internal.html", header, footer))
	tmpls["item.html"] = template.Must(template.ParseFiles(templateDir + "item.html"))
	tmpls["itemrowstart.html"] = template.Must(template.ParseFiles(templateDir + "itemrowstart.html"))
	tmpls["itemrowend.html"] = template.Must(template.ParseFiles(templateDir + "itemrowend.html"))
	store = sessions.NewCookieStore([]byte(cs))
	discordOauthConfig.ClientID = ci
	discordOauthConfig.ClientSecret = cs
	discordOauthConfig.RedirectURL = redirectURL + "/discordCallback"

	r := mux.NewRouter()
	r.HandleFunc("/", handleMain)
	r.HandleFunc("/logout", handleLogout)
	r.HandleFunc("/discordLogin", handlediscordLogin)
	r.HandleFunc("/discordCallback", handlediscordCallback)
	r.HandleFunc("/playsound", handlePlaySound)
	http.Handle("/", r)
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	http.ListenAndServe(":"+port, nil)
}

func handlePlaySound(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
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
	if user != nil && guild != nil && sound != nil && soundCollection != nil {
		go enqueuePlay(user, guild, soundCollection, sound)
	}
}

func handleMain(w http.ResponseWriter, r *http.Request) {
	session, _ := store.Get(r, "gidbig-session")
	if session.Values["discordUsername"] != nil {
		err := tmpls["internal.html"].ExecuteTemplate(w, "header", map[string]interface{}{})
		if err != nil {
			fmt.Println(err)
			return
		}

		var si []SoundItem
		for _, sc := range COLLECTIONS {
			for _, snd := range sc.Sounds {
				var newSoundItem SoundItem
				newSoundItem = SoundItem{
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

		for i, snd := range si {
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
		}

		err = tmpls["internal.html"].ExecuteTemplate(w, "footer", map[string]interface{}{})
		if err != nil {
			fmt.Println(err)
			return
		}
		return
	}
	tmpls["home.html"].ExecuteTemplate(w, "header", map[string]interface{}{})
	tmpls["home.html"].ExecuteTemplate(w, "footer", map[string]interface{}{})
}
func handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie := &http.Cookie{
		Name:   "gidbig-session",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	}
	http.SetCookie(w, cookie)
	http.Redirect(w, r, "/", http.StatusFound)
}
func handlediscordLogin(w http.ResponseWriter, r *http.Request) {
	b := make([]byte, 16)
	rand.Read(b)
	oauthStateString = base64.URLEncoding.EncodeToString(b)

	session, _ := store.Get(r, "gidbig-session")
	session.Values["state"] = oauthStateString
	session.Save(r, w)

	url := discordOauthConfig.AuthCodeURL(oauthStateString)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func handlediscordCallback(w http.ResponseWriter, r *http.Request) {
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

	token, err := discordOauthConfig.Exchange(oauth2.NoContext, r.FormValue("code"))
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
	session.Save(r, w)

	dg.Close()

	http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
}
