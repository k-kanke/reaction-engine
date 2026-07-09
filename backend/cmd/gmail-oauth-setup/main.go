// Command gmail-oauth-setup is a local-only, one-shot tool: it runs the
// OAuth2 authorization code flow against a user-created OAuth client
// (Google Cloud Console > APIs & Services > Credentials > OAuth client ID,
// type "Desktop app") and prints the resulting refresh token. It is never
// built into a container image or deployed -- the printed refresh token is
// what an operator pastes into `terraform apply
// -var="gmail_oauth_refresh_token=..."` (or Secret Manager directly) for
// cmd/gmail-sender's GMAIL_SEND_BACKEND=real path (see
// cmd/gmail-sender/README.md).
//
// The OAuth consent screen must be in "Testing" status with the sending
// Gmail account added as a test user, and the OAuth client's authorized
// redirect URIs must include http://localhost:<port>/callback for
// whatever --port this is run with. Testing-status refresh tokens expire
// after 7 days per Google's policy -- re-run this tool to mint a new one
// when cmd/gmail-sender starts failing with an invalid_grant error.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
)

func main() {
	clientID := flag.String("client-id", "", "OAuth client ID (from the GCP Console Desktop app credential)")
	clientSecret := flag.String("client-secret", "", "OAuth client secret (from the same credential)")
	port := flag.Int("port", 8085, "local port for the OAuth redirect; must match an authorized redirect URI on the OAuth client (http://localhost:<port>/callback)")
	flag.Parse()

	if *clientID == "" || *clientSecret == "" {
		log.Fatal("gmail-oauth-setup: --client-id and --client-secret are required")
	}

	redirectURL := fmt.Sprintf("http://localhost:%d/callback", *port)
	cfg := &oauth2.Config{
		ClientID:     *clientID,
		ClientSecret: *clientSecret,
		RedirectURL:  redirectURL,
		Scopes:       []string{gmail.GmailSendScope},
		Endpoint:     google.Endpoint,
	}

	// AccessTypeOffline requests a refresh token; ApprovalForce ensures one
	// is actually returned even if this Google account previously granted
	// this OAuth client consent (Google otherwise only issues a refresh
	// token on a account+client's very first consent).
	authURL := cfg.AuthCodeURL("state", oauth2.AccessTypeOffline, oauth2.ApprovalForce)

	fmt.Println("1. Open this URL in a browser signed into the sending Gmail account:")
	fmt.Println()
	fmt.Println("   " + authURL)
	fmt.Println()
	fmt.Println("2. Approve the consent screen. This tool will print the refresh token once redirected back.")
	fmt.Println()

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if errParam := r.URL.Query().Get("error"); errParam != "" {
			http.Error(w, "authorization denied", http.StatusBadRequest)
			errCh <- fmt.Errorf("authorization denied: %s", errParam)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			errCh <- fmt.Errorf("callback had no code parameter")
			return
		}
		fmt.Fprintln(w, "Authorized. You can close this tab and return to the terminal.")
		codeCh <- code
	})

	server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", *port), Handler: mux}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("local callback server: %w", err)
		}
	}()

	ctx := context.Background()

	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		log.Fatalf("gmail-oauth-setup: %v", err)
	}
	_ = server.Close()

	token, err := cfg.Exchange(ctx, code)
	if err != nil {
		log.Fatalf("gmail-oauth-setup: exchange code for token failed: %v", err)
	}
	if token.RefreshToken == "" {
		log.Fatal("gmail-oauth-setup: no refresh_token in response (this Google account may have already granted this OAuth client consent without ApprovalForce taking effect -- revoke access at https://myaccount.google.com/permissions and try again)")
	}

	fmt.Println()
	fmt.Println("refresh_token:")
	fmt.Println()
	fmt.Println("  " + token.RefreshToken)
	fmt.Println()
	fmt.Println("Store this as gmail_oauth_refresh_token (Terraform sensitive var / Secret Manager). It is not saved anywhere by this tool.")
}
