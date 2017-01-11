package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"

	"github.com/bwmarrin/discordgo"
	"github.com/gorilla/mux"
	"github.com/gorilla/sessions"
	"golang.org/x/oauth2"
)

const (
	defaultLayout = "templates/layout.html"
	templateDir   = "templates/"
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

// startWebServer
func startWebServer(port string, ci string, cs string, redirectURL string) {
	tmpls["home.html"] = template.Must(template.ParseFiles(templateDir+"home.html", defaultLayout))
	store = sessions.NewCookieStore([]byte(cs))
	discordOauthConfig.ClientID = ci
	discordOauthConfig.ClientSecret = cs
	discordOauthConfig.RedirectURL = redirectURL + "/discordCallback"

	r := mux.NewRouter()
	r.HandleFunc("/", handleMain)
	r.HandleFunc("/logout", handleLogout)
	r.HandleFunc("/discordLogin", handlediscordLogin)
	r.HandleFunc("/discordCallback", handlediscordCallback)

	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	http.Handle("/", r)
	http.ListenAndServe(":"+port, nil)
}

func handleMain(w http.ResponseWriter, r *http.Request) {
	session, _ := store.Get(r, "session-name")
	if session.Values["discordUsername"] != nil {
		fmt.Fprintf(w, "<html><body>you are logged in as %s. <a href=\"/logout\">logout</a></body></html>", session.Values["discordUsername"])
		return
	}
	tmpls["home.html"].ExecuteTemplate(w, "base", map[string]interface{}{})
}
func handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie := &http.Cookie{
		Name:   "session-name",
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

	session, _ := store.Get(r, "session-name")
	session.Values["state"] = oauthStateString
	session.Save(r, w)

	url := discordOauthConfig.AuthCodeURL(oauthStateString)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func handlediscordCallback(w http.ResponseWriter, r *http.Request) {
	session, err := store.Get(r, "session-name")
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
