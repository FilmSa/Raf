package app

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"log"
	"mime/multipart"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

//go:embed web/*
var assets embed.FS

const maxPhoto = 2 << 20

var uidPattern = regexp.MustCompile(`^[0-9A-F]{14}$`)

type App struct {
	cfg   Config
	store *Store
	face  *FaceClient
	auth  authState
}

func New(cfg Config, store *Store) *App {
	return &App{cfg: cfg, store: store, face: NewFaceClient(cfg.FaceURL), auth: authState{sessions: map[string]time.Time{}, attempts: map[string]attempt{}}}
}
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/access", a.access)
	mux.HandleFunc("POST /api/admin/login", a.login)
	mux.HandleFunc("POST /api/admin/logout", a.admin(a.logout))
	mux.HandleFunc("GET /api/admin/session", a.admin(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]bool{"ok": true}) }))
	mux.HandleFunc("GET /api/admin/users", a.admin(a.users))
	mux.HandleFunc("POST /api/admin/users", a.admin(a.createUser))
	mux.HandleFunc("PATCH /api/admin/users/{id}", a.admin(a.updateUser))
	mux.HandleFunc("DELETE /api/admin/users/{id}", a.admin(a.deleteUser))
	mux.HandleFunc("GET /api/admin/logs", a.admin(a.logs))
	mux.HandleFunc("GET /api/admin/stats", a.admin(a.stats))
	mux.HandleFunc("GET /api/admin/health", a.admin(a.health))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if err := a.store.db.PingContext(r.Context()); err != nil {
			writeJSON(w, 503, map[string]string{"status": "unavailable"})
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	web, _ := fs.Sub(assets, "web")
	files := http.FileServer(http.FS(web))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/styles.css" && r.URL.Path != "/app.js" {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' blob: data:; style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Print(err)
	}
}
func apiError(w http.ResponseWriter, code int, status, message string) {
	writeJSON(w, code, map[string]string{"error": status, "message": message})
}
func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
func parseForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxPhoto+(64<<10))
	return r.ParseMultipartForm(maxPhoto + (64 << 10))
}
func photoBytes(form *multipart.Form, jpegOnly bool) ([]byte, error) {
	files := form.File["photo"]
	if len(files) != 1 || files[0].Size <= 0 || files[0].Size > maxPhoto {
		return nil, fmt.Errorf("one photo up to 2 MB required")
	}
	f, err := files[0].Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxPhoto+1))
	if err != nil || len(b) > maxPhoto {
		return nil, fmt.Errorf("photo too large")
	}
	c, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || (format != "jpeg" && (jpegOnly || format != "png")) || c.Width < 32 || c.Height < 32 || int64(c.Width)*int64(c.Height) > 4_000_000 {
		return nil, fmt.Errorf("invalid image")
	}
	return b, nil
}

type AccessReply struct {
	Access bool   `json:"access"`
	Status string `json:"status"`
}

func (a *App) access(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	uid := ""
	user := User{}
	finish := func(status string) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		final, err := a.store.RecordDecision(ctx, user, uid, status, time.Since(started).Milliseconds())
		if err != nil {
			log.Printf("audit failure: %v", err)
			final = "server_error"
		}
		writeJSON(w, 200, AccessReply{Access: final == "success", Status: final})
	}
	if a.cfg.DeviceKey != "" && !sameSecret(r.Header.Get("X-Device-Key"), a.cfg.DeviceKey) && !(a.authorized(r) && sameOrigin(r)) {
		finish("unauthorized_device")
		return
	}
	if err := parseForm(w, r); err != nil {
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		finish("invalid_request")
		return
	}
	defer r.MultipartForm.RemoveAll()
	values := r.MultipartForm.Value["uid"]
	if len(values) != 1 || len(r.MultipartForm.Value) != 1 || len(r.MultipartForm.File) != 1 || len(r.MultipartForm.File["photo"]) != 1 {
		finish("invalid_request")
		return
	}
	uid = values[0]
	if !uidPattern.MatchString(uid) {
		uid = ""
		finish("invalid_uid")
		return
	}
	var err error
	user, err = a.store.UserByUID(r.Context(), uid)
	if errors.Is(err, sql.ErrNoRows) {
		finish("unknown_uid")
		return
	}
	if err != nil {
		finish("server_error")
		return
	}
	if !user.IsActive {
		finish("inactive_user")
		return
	}
	photo, err := photoBytes(r.MultipartForm, true)
	if err != nil {
		finish("invalid_request")
		return
	}
	result, err := a.face.Encode(r.Context(), photo)
	if err != nil {
		log.Printf("face service: %v", err)
		finish("service_unavailable")
		return
	}
	switch result.Status {
	case "no_face_detected", "multiple_faces":
		finish(result.Status)
		return
	case "invalid_image":
		finish("invalid_request")
		return
	case "service_error":
		finish("service_unavailable")
		return
	}
	if !validEncoding(user.Encoding) {
		finish("server_error")
		return
	}
	if cosine(user.Encoding, result.Encoding) < a.cfg.FaceThreshold {
		finish("face_mismatch")
		return
	}
	finish("success")
}
func (a *App) users(w http.ResponseWriter, r *http.Request) {
	u, err := a.store.Users(r.Context())
	if err != nil {
		apiError(w, 500, "server_error", "Не удалось загрузить пользователей")
		return
	}
	writeJSON(w, 200, u)
}
func (a *App) createUser(w http.ResponseWriter, r *http.Request) {
	if err := parseForm(w, r); err != nil {
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		apiError(w, 400, "invalid_request", "Проверьте файл: JPEG или PNG до 2 МБ")
		return
	}
	defer r.MultipartForm.RemoveAll()
	name := strings.TrimSpace(r.FormValue("full_name"))
	uid := r.FormValue("card_uid")
	if len([]rune(name)) < 2 || len([]rune(name)) > 120 || !uidPattern.MatchString(uid) {
		apiError(w, 400, "invalid_request", "Укажите ФИО и UID из 14 символов 0–9, A–F")
		return
	}
	if _, err := a.store.UserByUID(r.Context(), uid); err == nil {
		apiError(w, 409, "duplicate_uid", "Эта карта уже зарегистрирована")
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		apiError(w, 500, "server_error", "Ошибка базы данных")
		return
	}
	photo, err := photoBytes(r.MultipartForm, false)
	if err != nil {
		apiError(w, 400, "invalid_image", "Нужен JPEG или PNG до 2 МБ и 4 мегапикселей")
		return
	}
	result, err := a.face.Encode(r.Context(), photo)
	if err != nil {
		apiError(w, 503, "service_unavailable", "Сервис распознавания недоступен")
		return
	}
	if result.Status != "ok" {
		message := map[string]string{"no_face_detected": "Лицо не найдено. Выберите чёткий портрет анфас.", "multiple_faces": "На фото должно быть ровно одно лицо.", "invalid_image": "Не удалось прочитать изображение.", "service_error": "Ошибка сервиса распознавания."}[result.Status]
		code := 422
		if result.Status == "service_error" {
			code = 503
		}
		apiError(w, code, result.Status, message)
		return
	}
	u, err := a.store.CreateUser(r.Context(), name, uid, result.Encoding)
	if err != nil {
		if _, lookup := a.store.UserByUID(r.Context(), uid); lookup == nil {
			apiError(w, 409, "duplicate_uid", "Эта карта уже зарегистрирована")
		} else {
			apiError(w, 500, "server_error", "Не удалось сохранить пользователя")
		}
		return
	}
	writeJSON(w, 201, u)
}
func pathID(r *http.Request) (int64, error) {
	n, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if n <= 0 {
		return 0, fmt.Errorf("invalid id")
	}
	return n, err
}
func (a *App) updateUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	var input struct {
		IsActive *bool `json:"is_active"`
	}
	if err != nil || readJSON(w, r, &input) != nil || input.IsActive == nil {
		apiError(w, 400, "invalid_request", "Укажите статус пользователя")
		return
	}
	if err := a.store.SetActive(r.Context(), id, *input.IsActive); err != nil {
		mutationError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *App) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		apiError(w, 400, "invalid_request", "Некорректный ID")
		return
	}
	if err := a.store.DeleteUser(r.Context(), id); err != nil {
		mutationError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func mutationError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		apiError(w, 404, "not_found", "Пользователь не найден")
	} else {
		apiError(w, 500, "server_error", "Не удалось сохранить изменения")
	}
}
func (a *App) logs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := LogFilter{Query: strings.TrimSpace(q.Get("q")), Status: q.Get("status"), Date: q.Get("date"), Limit: 50}
	if f.Date != "" {
		if _, err := time.Parse("2006-01-02", f.Date); err != nil {
			apiError(w, 400, "invalid_date", "Некорректная дата")
			return
		}
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			apiError(w, 400, "invalid_limit", "Лимит от 1 до 100")
			return
		}
		f.Limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			apiError(w, 400, "invalid_offset", "Некорректное смещение")
			return
		}
		f.Offset = n
	}
	p, err := a.store.Logs(r.Context(), f)
	if err != nil {
		apiError(w, 500, "server_error", "Не удалось загрузить журнал")
		return
	}
	writeJSON(w, 200, p)
}
func (a *App) stats(w http.ResponseWriter, r *http.Request) {
	s, err := a.store.Stats(r.Context())
	if err != nil {
		apiError(w, 500, "server_error", "Ошибка статистики")
		return
	}
	writeJSON(w, 200, s)
}
func (a *App) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	writeJSON(w, 200, map[string]any{"database": a.store.db.PingContext(ctx) == nil, "face_service": a.face.Healthy(ctx), "device_key_required": a.cfg.DeviceKey != "", "threshold": a.cfg.FaceThreshold})
}
