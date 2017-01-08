package main

import (
	"fmt"
	"net/http"

	"github.com/bwmarrin/discordgo"
	"golang.org/x/oauth2"
)

const htmlIndex = `<html><body>
<a href="/discordLogin">Log in with discord</a>
</body></html>
`

var (
	discordOauthConfig = &oauth2.Config{
		RedirectURL:  "",
		ClientID:     "",
		ClientSecret: "",
		Scopes: []string{"connections",
			"email",
			"identify",
			"guilds"},
		Endpoint: *endp,
	}
	// Some random string, random for each request
	oauthStateString = "random"
	endp             = &oauth2.Endpoint{
		AuthURL:  "https://discordapp.com/api/oauth2/authorize",
		TokenURL: "https://discordapp.com/api/oauth2/token",
	}
)

// startWebServer with the port provided
func startWebServer(port string, ci string, cs string, redirectURL string) {
	discordOauthConfig.ClientID = ci
	discordOauthConfig.ClientSecret = cs
	discordOauthConfig.RedirectURL = redirectURL + "/discordCallback"
	http.HandleFunc("/", handleMain)
	http.HandleFunc("/discordLogin", handlediscordLogin)
	http.HandleFunc("/discordCallback", handlediscordCallback)
	fmt.Println(http.ListenAndServe(":"+port, nil))
}

func handleMain(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, htmlIndex)
}

func handlediscordLogin(w http.ResponseWriter, r *http.Request) {
	url := discordOauthConfig.AuthCodeURL(oauthStateString)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func handlediscordCallback(w http.ResponseWriter, r *http.Request) {
	state := r.FormValue("state")
	if state != oauthStateString {
		fmt.Printf("invalid oauth state, expected '%s', got '%s'\n", oauthStateString, state)
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return
	}

	code := r.FormValue("code")
	token, err := discordOauthConfig.Exchange(oauth2.NoContext, code)
	if err != nil {
		fmt.Println("Code exchange failed with ", err)
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return
	}

	dg, err := discordgo.New("Bearer " + token.AccessToken)
	if err != nil {
		fmt.Println("Error while creating discord session: ", err)
		return
	}
	user, _ := dg.User("@me")
	fmt.Fprintf(w, user.Username)
}
