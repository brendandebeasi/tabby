package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	host := flag.String("host", "127.0.0.1", "HTTP server host (loopback only)")
	port := flag.Int("port", 8080, "HTTP server port")
	sessionID := flag.String("session", "", "tmux session ID")
	tokenFile := flag.String("token-file", "", "token file path (default: ~/.config/tabby/web-token)")
	regenerateToken := flag.Bool("regenerate-token", false, "regenerate auth token on startup")
	authUser := flag.String("auth-user", "", "required username for web access")
	authPass := flag.String("auth-pass", "", "required password for web access")
	flag.Parse()

	tokenPath := *tokenFile
	if tokenPath == "" {
		var err error
		tokenPath, err = DefaultTokenPath()
		if err != nil {
			log.Fatalf("failed to resolve token path: %v", err)
		}
	}

	var token string
	var err error
	if *regenerateToken {
		token, err = RegenerateToken(tokenPath)
	} else {
		token, err = LoadOrGenerateToken(tokenPath)
	}
	if err != nil {
		log.Fatalf("failed to load token: %v", err)
	}

	if *authUser == "" || *authPass == "" {
		log.Fatalf("auth-user and auth-pass are required")
	}

	server := NewServer(ServerConfig{
		Host:      *host,
		Port:      *port,
		SessionID: *sessionID,
		Token:     token,
		AuthUser:  *authUser,
		AuthPass:  *authPass,
	})

	if err := server.Start(); err != nil {
		log.Fatalf("server failed to start: %v", err)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	server.Stop()
}
