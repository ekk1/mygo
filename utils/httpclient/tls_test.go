package httpclient

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestMutualTLS(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-client"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	clientCert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(clientCert)
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.PeerCertificates) != 1 || r.TLS.PeerCertificates[0].Subject.CommonName != "test-client" {
			t.Error("missing client identity")
		}
		io.WriteString(w, `{"authenticated":true}`)
	}))
	s.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}
	s.StartTLS()
	defer s.Close()
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile, caFile := filepath.Join(dir, "client.pem"), filepath.Join(dir, "key.pem"), filepath.Join(dir, "ca.pem")
	for file, block := range map[string]*pem.Block{certFile: {Type: "CERTIFICATE", Bytes: der}, keyFile: {Type: "PRIVATE KEY", Bytes: keyDER}, caFile: {Type: "CERTIFICATE", Bytes: s.Certificate().Raw}} {
		if err := os.WriteFile(file, pem.EncodeToMemory(block), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := Config{CAFile: caFile, CertFile: certFile, KeyFile: keyFile, ProxyURL: "-"}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	var out struct{ Authenticated bool }
	if _, err := c.JSON(context.Background(), "GET", s.URL, nil, &out); err != nil || !out.Authenticated {
		t.Fatalf("mTLS: %+v %v", out, err)
	}
	cfg.CertFile = ""
	cfg.KeyFile = ""
	c, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	if _, err := c.JSON(context.Background(), "GET", s.URL, nil, nil); err == nil {
		t.Fatal("accepted request without certificate")
	}
	if err := os.WriteFile(caFile, []byte("not a PEM certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg); err == nil {
		t.Fatal("invalid CA accepted")
	}
}

func TestTimeoutAndConcurrentHelpers(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			<-r.Context().Done()
			return
		}
		io.WriteString(w, `{"ok":true}`)
	}))
	defer s.Close()
	c, err := New(Config{Timeout: 20 * time.Millisecond, ProxyURL: "-"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	if _, err := c.JSON(context.Background(), "GET", s.URL+"/slow", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var out struct{ OK bool }
			var err error
			if i%2 == 0 {
				_, err = JSON(context.Background(), "GET", s.URL, nil, &out)
			} else {
				_, err = Form(context.Background(), "POST", s.URL, nil, &out)
			}
			if err != nil || !out.OK {
				t.Errorf("concurrent request: %+v %v", out, err)
			}
		}(i)
	}
	wg.Wait()
}
