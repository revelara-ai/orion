package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/revelara-ai/orion/internal/polaris"
)

// credentialsDir is separate from the Context Store DB and outside any path the
// sandbox binds — so the token is never in the store and never sandbox-reachable.
func credentialsDir() (string, error) {
	dir, err := resolveDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "credentials"), nil
}

func polarisURL(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if u := os.Getenv("ORION_POLARIS_URL"); u != "" {
		return u
	}
	return "https://app.revelara.ai"
}

// cmdLogin authenticates to Polaris and caches the token (0600, outside the store).
func cmdLogin(args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	url := fs.String("url", "", "Polaris base URL (or $ORION_POLARIS_URL)")
	username := fs.String("username", "", "username")
	password := fs.String("password", "", "password")
	token := fs.String("token", "", "set a token directly (headless/short-lived)")
	org := fs.String("org", "", "organization")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	base := polarisURL(*url)

	dir, err := credentialsDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion login:", err)
		return 1
	}
	store, err := polaris.NewTokenStore(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion login:", err)
		return 1
	}

	var tok polaris.Token
	if *token != "" {
		tok = polaris.Token{AccessToken: *token, BaseURL: base, Org: *org}
	} else {
		if *username == "" || *password == "" {
			fmt.Fprintln(os.Stderr, "orion login: provide --token, or --username and --password")
			return 2
		}
		tok, err = polaris.NewClient(base).Login(context.Background(), *username, *password)
		if err != nil {
			fmt.Fprintln(os.Stderr, "orion login:", err)
			return 1
		}
		tok.Org = *org
	}
	if err := store.Save(tok); err != nil {
		fmt.Fprintln(os.Stderr, "orion login:", err)
		return 1
	}
	fmt.Printf("logged in to %s (credential cached at %s)\n", base, store.Path())
	return 0
}

// cmdStatus reports the Polaris connection.
func cmdStatus(_ []string) int {
	dir, err := credentialsDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion status:", err)
		return 1
	}
	store, err := polaris.NewTokenStore(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion status:", err)
		return 1
	}
	tok, ok, err := store.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion status:", err)
		return 1
	}
	if !ok {
		fmt.Println("Polaris: not logged in")
		return 1
	}
	id, err := polaris.NewClient(tok.BaseURL).Me(context.Background(), tok.AccessToken)
	if err != nil {
		// Offline-tolerant: a cached credential exists but the server is unreachable.
		fmt.Printf("Polaris: cached credential present for %s (server unreachable)\n", tok.BaseURL)
		return 0
	}
	who := id.Email
	if who == "" {
		who = "authenticated"
	}
	fmt.Printf("Polaris: connected as %s (%s)\n", who, tok.BaseURL)
	return 0
}

// cmdLogout erases the cached credential.
func cmdLogout(_ []string) int {
	dir, err := credentialsDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion logout:", err)
		return 1
	}
	store, err := polaris.NewTokenStore(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion logout:", err)
		return 1
	}
	if tok, ok, _ := store.Load(); ok {
		_ = polaris.NewClient(tok.BaseURL).Logout(context.Background(), tok.AccessToken)
	}
	if err := store.Clear(); err != nil {
		fmt.Fprintln(os.Stderr, "orion logout:", err)
		return 1
	}
	fmt.Println("logged out")
	return 0
}
