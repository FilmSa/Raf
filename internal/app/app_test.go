package app

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testUID = "04A1B2C3D4E5F6"

func vector(index int) []float64 { v := make([]float64, 128); v[index] = 1; return v }
func fixture(t *testing.T, face http.HandlerFunc) (*App, http.Handler) {
	t.Helper()
	remote := httptest.NewServer(face)
	t.Cleanup(remote.Close)
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	a := New(Config{AdminPassword: "test-password-123", FaceURL: remote.URL, FaceThreshold: 0.363}, s)
	return a, a.Handler()
}
func faceOK(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, FaceResult{Status: "ok", Encoding: vector(0)})
}
func testPhoto(t *testing.T, format string) []byte {
	t.Helper()
	var out bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	var err error
	if format == "png" {
		err = png.Encode(&out, img)
	} else {
		err = jpeg.Encode(&out, img, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func multipartRequest(t *testing.T, path string, fields map[string]string, photo []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if photo != nil {
		part, err := writer.CreateFormFile("photo", "photo.jpg")
		if err != nil {
			t.Fatal(err)
		}
		part.Write(photo)
	}
	writer.Close()
	r := httptest.NewRequest("POST", path, &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	return r
}
func perform(t *testing.T, h http.Handler, r *http.Request, v any) *httptest.ResponseRecorder {
	t.Helper()
	res := httptest.NewRecorder()
	h.ServeHTTP(res, r)
	if v != nil {
		if err := json.Unmarshal(res.Body.Bytes(), v); err != nil {
			t.Fatalf("bad JSON: %s: %v", res.Body, err)
		}
	}
	return res
}
func loginCookie(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/admin/login", strings.NewReader(`{"password":"test-password-123"}`))
	r.Header.Set("Content-Type", "application/json")
	res := perform(t, h, r, nil)
	if res.Code != 200 {
		t.Fatalf("login: %s", res.Body)
	}
	return res.Result().Cookies()[0]
}
func seedUser(t *testing.T, a *App) User {
	t.Helper()
	u, err := a.store.CreateUser(context.Background(), "Иванов Иван", testUID, vector(0))
	if err != nil {
		t.Fatal(err)
	}
	return u
}
func TestAccessContractAndAudit(t *testing.T) {
	tests := []struct {
		name, status, faceStatus string
		uid                      string
		known, blocked, mismatch bool
	}{
		{name: "success", status: "success", known: true},
		{name: "unknown", status: "unknown_uid"},
		{name: "blocked", status: "inactive_user", known: true, blocked: true},
		{name: "mismatch", status: "face_mismatch", known: true, mismatch: true},
		{name: "no face", status: "no_face_detected", faceStatus: "no_face_detected", known: true},
		{name: "multiple faces", status: "multiple_faces", faceStatus: "multiple_faces", known: true},
		{name: "bad image", status: "invalid_request", faceStatus: "invalid_image", known: true},
		{name: "service error", status: "service_unavailable", faceStatus: "service_error", known: true},
		{name: "invalid UID", status: "invalid_uid", uid: "abcd"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			a, h := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				status := tc.faceStatus
				if status == "" {
					status = "ok"
				}
				v := vector(0)
				if tc.mismatch {
					v = vector(1)
				}
				writeJSON(w, 200, FaceResult{Status: status, Encoding: v})
			})
			if tc.known {
				u := seedUser(t, a)
				if tc.blocked {
					a.store.SetActive(context.Background(), u.ID, false)
				}
			}
			uid := tc.uid
			if uid == "" {
				uid = testUID
			}
			r := multipartRequest(t, "/api/v1/access", map[string]string{"uid": uid}, testPhoto(t, "jpeg"))
			var reply AccessReply
			res := perform(t, h, r, &reply)
			if res.Code != 200 || reply.Status != tc.status || reply.Access != (tc.status == "success") {
				t.Fatalf("response %d %+v", res.Code, reply)
			}
			var shape map[string]any
			json.Unmarshal(res.Body.Bytes(), &shape)
			if len(shape) != 2 {
				t.Fatalf("contract includes extra keys: %v", shape)
			}
			if (!tc.known || tc.blocked) && calls != 0 {
				t.Fatal("face service called before checking card")
			}
			page, err := a.store.Logs(context.Background(), LogFilter{Limit: 10})
			if err != nil || page.Total != 1 || page.Items[0].Result != tc.status {
				t.Fatalf("audit: %+v %v", page, err)
			}
		})
	}
}
func TestMalformedUploadsNeverGrant(t *testing.T) {
	a, h := fixture(t, faceOK)
	seedUser(t, a)
	requests := []*http.Request{
		httptest.NewRequest("POST", "/api/v1/access", strings.NewReader("bad body")),
		multipartRequest(t, "/api/v1/access", map[string]string{"uid": testUID}, nil),
		multipartRequest(t, "/api/v1/access", map[string]string{"uid": testUID}, []byte("bad jpeg")),
		multipartRequest(t, "/api/v1/access", map[string]string{"uid": testUID}, testPhoto(t, "png")),
		multipartRequest(t, "/api/v1/access", map[string]string{"uid": testUID, "extra": "oops"}, testPhoto(t, "jpeg")),
		multipartRequest(t, "/api/v1/access", map[string]string{"uid": testUID}, make([]byte, maxPhoto+1)),
	}
	for i, r := range requests {
		var reply AccessReply
		res := perform(t, h, r, &reply)
		if res.Code != 200 || reply.Access || reply.Status != "invalid_request" {
			t.Fatalf("upload %d: %d %+v", i, res.Code, reply)
		}
	}
}
func TestUnavailableOrInvalidFaceService(t *testing.T) {
	for _, mode := range []string{"unavailable", "invalid vector", "unknown status", "broken JSON"} {
		t.Run(mode, func(t *testing.T) {
			a, h := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "unavailable":
					w.WriteHeader(503)
				case "invalid vector":
					writeJSON(w, 200, FaceResult{Status: "ok", Encoding: []float64{1}})
				case "unknown status":
					writeJSON(w, 200, FaceResult{Status: "unexpected"})
				default:
					io.WriteString(w, "bad")
				}
			})
			seedUser(t, a)
			var reply AccessReply
			perform(t, h, multipartRequest(t, "/api/v1/access", map[string]string{"uid": testUID}, testPhoto(t, "jpeg")), &reply)
			if reply.Access || reply.Status != "service_unavailable" {
				t.Fatalf("unsafe: %+v", reply)
			}
		})
	}
}
func TestUserLifecycleAndProtectedAPI(t *testing.T) {
	a, h := fixture(t, faceOK)
	for _, path := range []string{"/api/admin/users", "/api/admin/logs", "/api/admin/stats", "/api/admin/health"} {
		if res := perform(t, h, httptest.NewRequest("GET", path, nil), nil); res.Code != 401 {
			t.Fatalf("unprotected %s", path)
		}
	}
	cookie := loginCookie(t, h)
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe cookie")
	}
	r := multipartRequest(t, "/api/admin/users", map[string]string{"full_name": "Иван Иванов", "card_uid": testUID}, testPhoto(t, "png"))
	r.AddCookie(cookie)
	var user User
	res := perform(t, h, r, &user)
	if res.Code != 201 || user.ID == 0 {
		t.Fatalf("registration: %s", res.Body)
	}
	if bytes.Contains(res.Body.Bytes(), []byte("encoding")) {
		t.Fatal("biometric data exposed")
	}
	r = multipartRequest(t, "/api/admin/users", map[string]string{"full_name": "Дубликат", "card_uid": testUID}, testPhoto(t, "jpeg"))
	r.AddCookie(cookie)
	if res := perform(t, h, r, nil); res.Code != 409 {
		t.Fatalf("duplicate: %s", res.Body)
	}
	r = httptest.NewRequest("PATCH", "/api/admin/users/"+strconv.FormatInt(user.ID, 10), strings.NewReader(`{"is_active":false}`))
	r.AddCookie(cookie)
	if res := perform(t, h, r, nil); res.Code != 200 {
		t.Fatalf("block: %s", res.Body)
	}
	var reply AccessReply
	perform(t, h, multipartRequest(t, "/api/v1/access", map[string]string{"uid": testUID}, testPhoto(t, "jpeg")), &reply)
	if reply.Status != "inactive_user" {
		t.Fatal(reply)
	}
	r = httptest.NewRequest("DELETE", "/api/admin/users/"+strconv.FormatInt(user.ID, 10), nil)
	r.AddCookie(cookie)
	if res := perform(t, h, r, nil); res.Code != 200 {
		t.Fatalf("delete: %s", res.Body)
	}
	page, _ := a.store.Logs(context.Background(), LogFilter{Limit: 10})
	if page.Total != 1 || page.Items[0].FullName != "Иван Иванов" {
		t.Fatal("deletion damaged audit history")
	}
	r = httptest.NewRequest("POST", "/api/admin/logout", nil)
	r.AddCookie(cookie)
	perform(t, h, r, nil)
	r = httptest.NewRequest("GET", "/api/admin/users", nil)
	r.AddCookie(cookie)
	if perform(t, h, r, nil).Code != 401 {
		t.Fatal("session survived logout")
	}
}
func TestOriginAndDeviceKey(t *testing.T) {
	a, h := fixture(t, faceOK)
	a.cfg.DeviceKey = "device-secret"
	seedUser(t, a)
	var reply AccessReply
	perform(t, h, multipartRequest(t, "/api/v1/access", map[string]string{"uid": testUID}, testPhoto(t, "jpeg")), &reply)
	if reply.Status != "unauthorized_device" {
		t.Fatal(reply)
	}
	r := multipartRequest(t, "/api/v1/access", map[string]string{"uid": testUID}, testPhoto(t, "jpeg"))
	r.Header.Set("X-Device-Key", "device-secret")
	perform(t, h, r, &reply)
	if !reply.Access {
		t.Fatal(reply)
	}
	cookie := loginCookie(t, h)
	r = multipartRequest(t, "/api/v1/access", map[string]string{"uid": testUID}, testPhoto(t, "jpeg"))
	r.AddCookie(cookie)
	perform(t, h, r, &reply)
	if !reply.Access {
		t.Fatal("admin terminal rejected")
	}
	r = httptest.NewRequest("PATCH", "/api/admin/users/1", strings.NewReader(`{"is_active":false}`))
	r.AddCookie(cookie)
	r.Header.Set("Origin", "https://evil.example")
	if perform(t, h, r, nil).Code != 403 {
		t.Fatal("cross-origin mutation accepted")
	}
}
func TestBlockDuringRecognition(t *testing.T) {
	var a *App
	a, h := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		a.store.SetActive(context.Background(), 1, false)
		faceOK(w, r)
	})
	seedUser(t, a)
	var reply AccessReply
	perform(t, h, multipartRequest(t, "/api/v1/access", map[string]string{"uid": testUID}, testPhoto(t, "jpeg")), &reply)
	if reply.Access || reply.Status != "inactive_user" {
		t.Fatalf("block during inference ignored: %+v", reply)
	}
}
func TestAuditFailureNeverGrants(t *testing.T) {
	a, h := fixture(t, faceOK)
	seedUser(t, a)
	a.store.db.Exec("DROP TABLE logs")
	var reply AccessReply
	res := perform(t, h, multipartRequest(t, "/api/v1/access", map[string]string{"uid": testUID}, testPhoto(t, "jpeg")), &reply)
	if res.Code != 200 || reply.Access || reply.Status != "server_error" {
		t.Fatal("grant without audit")
	}
}
func TestRussianSearchAndMoscowDayBoundaries(t *testing.T) {
	a, _ := fixture(t, faceOK)
	ctx := context.Background()
	day, _ := time.ParseInLocation("2006-01-02", "2026-10-07", moscow)
	for i, ts := range []time.Time{day.Add(-time.Nanosecond), day, day.Add(time.Millisecond), day.AddDate(0, 0, 1).Add(-time.Nanosecond), day.AddDate(0, 0, 1)} {
		_, err := a.store.db.Exec("INSERT INTO logs(timestamp,card_uid,full_name,result) VALUES(?,?,?,?)", ts.UTC().Format(timestampFormat), testUID, "ИВАНОВ Иван", []string{"success", "success", "face_mismatch", "success", "success"}[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	page, err := a.store.Logs(ctx, LogFilter{Date: "2026-10-07", Query: "иванов", Limit: 10})
	if err != nil || page.Total != 3 {
		t.Fatalf("date or Cyrillic search: %+v %v", page, err)
	}
	page, err = a.store.Logs(ctx, LogFilter{Date: "2026-10-07", Status: "denied", Limit: 10})
	if err != nil || page.Total != 1 {
		t.Fatal("denied filter")
	}
}
func TestLoginThrottleAndStrictJSON(t *testing.T) {
	_, h := fixture(t, faceOK)
	for i := 0; i < 9; i++ {
		r := httptest.NewRequest("POST", "/api/admin/login", strings.NewReader(`{"password":"wrong-password"}`))
		res := perform(t, h, r, nil)
		expected := 401
		if i == 8 {
			expected = 429
		}
		if res.Code != expected {
			t.Fatalf("attempt %d: %d", i, res.Code)
		}
	}
	_, h = fixture(t, faceOK)
	cookie := loginCookie(t, h)
	for _, body := range []string{`{}`, `{"is_active":false,"unexpected":1}`, `{"is_active":false} {}`} {
		r := httptest.NewRequest("PATCH", "/api/admin/users/1", strings.NewReader(body))
		r.AddCookie(cookie)
		if perform(t, h, r, nil).Code != 400 {
			t.Fatal("malformed update accepted")
		}
	}
}
