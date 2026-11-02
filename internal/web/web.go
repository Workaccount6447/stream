// Package web renders the small HTML pages the streaming service shows to
// browsers (expired link, errors) instead of bare text responses.
package web

import (
	"embed"
	"html/template"
	"net/http"

	"EverythingSuckz/fsb/config"
)

//go:embed templates/*.html
var files embed.FS

var tpl = template.Must(template.ParseFS(files, "templates/*.html"))

type PageData struct {
	BotName    string
	UpdatesURL string
	Title      string
	Message    string
	FileName   string
	Code       int
}

func base(code int, title, msg string) PageData {
	d := PageData{BotName: config.ValueOf.BotName, Title: title, Message: msg, Code: code}
	if config.ValueOf.UpdatesChannel != "" {
		d.UpdatesURL = "https://t.me/" + config.ValueOf.UpdatesChannel
	}
	return d
}

func render(w http.ResponseWriter, name string, code int, d PageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_ = tpl.ExecuteTemplate(w, name, d)
}

// Expired is shown (HTTP 410) when a link's file was removed after its time
// limit, or the owner revoked it.
func Expired(w http.ResponseWriter, fileName string) {
	d := base(http.StatusGone, "This link has expired", "The file was automatically removed from storage after its time limit, or the link was revoked by its owner.")
	d.FileName = fileName
	render(w, "expired.html", http.StatusGone, d)
}

func Error(w http.ResponseWriter, code int, title, msg string) {
	render(w, "error.html", code, base(code, title, msg))
}
