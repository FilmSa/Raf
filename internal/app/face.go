package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"
)

type FaceResult struct {
	Status   string    `json:"status"`
	Encoding []float64 `json:"encoding"`
}
type FaceClient struct {
	URL  string
	HTTP *http.Client
}

func NewFaceClient(url string) *FaceClient {
	return &FaceClient{URL: url, HTTP: &http.Client{Timeout: 15 * time.Second}}
}
func (f *FaceClient) Encode(ctx context.Context, photo []byte) (FaceResult, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", f.URL+"/encode", bytes.NewReader(photo))
	if err != nil {
		return FaceResult{}, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	res, err := f.HTTP.Do(req)
	if err != nil {
		return FaceResult{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return FaceResult{}, fmt.Errorf("face service HTTP %d", res.StatusCode)
	}
	var result FaceResult
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&result); err != nil {
		return result, err
	}
	if result.Status == "ok" && !validEncoding(result.Encoding) {
		return result, fmt.Errorf("invalid face embedding")
	}
	switch result.Status {
	case "ok", "no_face_detected", "multiple_faces", "invalid_image", "service_error":
		return result, nil
	default:
		return result, fmt.Errorf("unknown face status")
	}
}
func (f *FaceClient) Healthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", f.URL+"/health", nil)
	if err != nil {
		return false
	}
	res, err := f.HTTP.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	return res.StatusCode == http.StatusOK
}
func validEncoding(v []float64) bool {
	if len(v) != 128 {
		return false
	}
	sum := 0.0
	for _, n := range v {
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return false
		}
		sum += n * n
	}
	return sum > 1e-12 && !math.IsInf(sum, 0)
}
func cosine(a, b []float64) float64 {
	if !validEncoding(a) || !validEncoding(b) {
		return -1
	}
	dot, na, nb := 0.0, 0.0, 0.0
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	return dot / math.Sqrt(na*nb)
}
