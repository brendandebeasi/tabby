package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/brendandebeasi/tabby/pkg/paths"
	"github.com/skip2/go-qrcode"
)

func DefaultTokenPath() (string, error) {
	return paths.StatePath("web-token"), nil
}

func LoadOrGenerateToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		token := strings.TrimSpace(string(data))
		if token != "" {
			return token, nil
		}
	}
	return RegenerateToken(path)
}

func RegenerateToken(path string) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}

	if err := os.WriteFile(path, []byte(token+"\n"), 0600); err != nil {
		return "", err
	}
	return token, nil
}

func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(buf), nil
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !s.validateAuth(r, true) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	wsURL := fmt.Sprintf("ws://%s/ws?token=%s&user=%s&pass=%s", r.Host, s.cfg.Token, url.QueryEscape(s.cfg.AuthUser), url.QueryEscape(s.cfg.AuthPass))
	png, err := qrcode.Encode(wsURL, qrcode.Medium, 256)
	if err != nil {
		http.Error(w, "failed to generate qr code", http.StatusInternalServerError)
		return
	}

	encoded := base64.StdEncoding.EncodeToString(png)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, connectPageHTML, encoded, wsURL)
}

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

const connectPageHTML = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>Tabby Web Connect</title>
    <style>
      body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 32px; }
      .container { max-width: 640px; margin: 0 auto; }
      .qr { width: 256px; height: 256px; border: 1px solid #ddd; padding: 8px; }
      code { display: block; margin-top: 12px; padding: 12px; background: #f6f6f6; border-radius: 8px; }
    </style>
  </head>
  <body>
    <div class="container">
      <h1>Tabby Web Connect</h1>
      <p>Scan this QR code with your phone to connect.</p>
      <p><strong>Local only.</strong> This bridge only accepts loopback connections.</p>
      <img class="qr" src="data:image/png;base64,%s" alt="QR code" />
      <p>Or open this URL on your phone:</p>
      <code>%s</code>
    </div>
  </body>
</html>
`
