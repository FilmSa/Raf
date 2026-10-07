package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr, Database, FaceURL, AdminPassword, DeviceKey string
	FaceThreshold                                     float64
	SecureCookie                                      bool
}

func LoadConfig() (Config, error) {
	c := Config{Addr: env("APP_ADDR", "127.0.0.1:8080"), Database: env("DB_PATH", "data/access.db"),
		FaceURL:       strings.TrimRight(env("FACE_SERVICE_URL", "http://127.0.0.1:8001"), "/"),
		AdminPassword: os.Getenv("ADMIN_PASSWORD"), DeviceKey: os.Getenv("DEVICE_API_KEY"),
		SecureCookie: os.Getenv("COOKIE_SECURE") == "true"}
	var err error
	c.FaceThreshold, err = strconv.ParseFloat(env("FACE_THRESHOLD", "0.363"), 64)
	if err != nil || !(c.FaceThreshold > 0 && c.FaceThreshold <= 1) {
		return c, fmt.Errorf("FACE_THRESHOLD must be in (0, 1]")
	}
	if c.AdminPassword == "" {
		c.AdminPassword = randomToken(16)
	}
	if len(c.AdminPassword) < 10 {
		return c, fmt.Errorf("ADMIN_PASSWORD must contain at least 10 characters")
	}
	u, parseErr := url.Parse(c.FaceURL)
	if parseErr != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" {
		return c, fmt.Errorf("FACE_SERVICE_URL must be an absolute HTTP(S) URL without query or fragment")
	}
	return c, nil
}

func env(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return fallback
}
func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

var moscow = time.FixedZone("MSK", 3*60*60)
