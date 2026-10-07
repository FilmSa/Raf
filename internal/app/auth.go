package app

import (
	"crypto/sha256"
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type attempt struct {
	Count int
	Until time.Time
}
type authState struct {
	mu       sync.Mutex
	sessions map[string]time.Time
	attempts map[string]attempt
}

func sameSecret(a, b string) bool {
	x, y := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(x[:], y[:]) == 1
}
func (a *App) authorized(r *http.Request) bool {
	c, err := r.Cookie("gate_session")
	if err != nil {
		return false
	}
	a.auth.mu.Lock()
	defer a.auth.mu.Unlock()
	t, ok := a.auth.sessions[c.Value]
	if ok && time.Now().Before(t) {
		return true
	}
	delete(a.auth.sessions, c.Value)
	return false
}
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		return err == nil && u.Host == r.Host && (u.Scheme == "http" || u.Scheme == "https")
	}
	return true
}
func (a *App) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.authorized(r) {
			apiError(w, 401, "unauthorized", "Войдите в панель администратора")
			return
		}
		if r.Method != "GET" && !sameOrigin(r) {
			apiError(w, 403, "forbidden", "Недопустимый источник запроса")
			return
		}
		next(w, r)
	}
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		apiError(w, 403, "forbidden", "Недопустимый источник запроса")
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	a.auth.mu.Lock()
	now := time.Now()
	for key, item := range a.auth.attempts {
		if now.After(item.Until) {
			delete(a.auth.attempts, key)
		}
	}
	item := a.auth.attempts[ip]
	if item.Count >= 8 && now.Before(item.Until) {
		a.auth.mu.Unlock()
		apiError(w, 429, "rate_limited", "Слишком много попыток. Повторите через минуту.")
		return
	}
	if item.Count == 0 {
		item.Until = now.Add(time.Minute)
	}
	item.Count++
	a.auth.attempts[ip] = item
	a.auth.mu.Unlock()
	var input struct {
		Password string `json:"password"`
	}
	if err := readJSON(w, r, &input); err != nil {
		apiError(w, 400, "invalid_request", "Некорректный запрос")
		return
	}
	if !sameSecret(input.Password, a.cfg.AdminPassword) {
		apiError(w, 401, "invalid_password", "Неверный пароль")
		return
	}
	token := randomToken(32)
	expires := time.Now().Add(8 * time.Hour)
	a.auth.mu.Lock()
	for key, t := range a.auth.sessions {
		if now.After(t) {
			delete(a.auth.sessions, key)
		}
	}
	a.auth.sessions[token] = expires
	delete(a.auth.attempts, ip)
	a.auth.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "gate_session", Value: token, Path: "/", Expires: expires, MaxAge: 8 * 3600, HttpOnly: true, Secure: a.cfg.SecureCookie, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("gate_session"); err == nil {
		a.auth.mu.Lock()
		delete(a.auth.sessions, c.Value)
		a.auth.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "gate_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.cfg.SecureCookie, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]bool{"ok": true})
}
