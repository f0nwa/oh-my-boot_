package ldapclient

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"
)

func generateTestTLS(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return cert, string(certPEM)
}

func startFakeLDAPServer(t *testing.T, useStartTLS bool, cert tls.Certificate, guid []byte) (string, func()) {
	t.Helper()
	var ln net.Listener
	var err error
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}}
	if useStartTLS {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	} else {
		ln, err = tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	}
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleFakeLDAPConn(conn, useStartTLS, tlsCfg, guid)
		}
	}()
	return ln.Addr().String(), func() {
		_ = ln.Close()
		<-done
	}
}

func handleFakeLDAPConn(conn net.Conn, useStartTLS bool, tlsCfg *tls.Config, guid []byte) {
	defer conn.Close()
	for {
		pkt, err := readBERPacket(conn)
		if err != nil || len(pkt.Children) < 2 {
			return
		}
		id, _ := pkt.Children[0].intVal()
		op := pkt.Children[1]
		switch op.Tag {
		case appExtendedRequest:
			if useStartTLS {
				resp := encodeSequence(tagSequence,
					encodeInt(tagInteger, id),
					encodeSequence(appExtendedResponse,
						encodeInt(tagEnumerated, 0),
						encodeString(""),
						encodeString(""),
					),
				)
				_, _ = conn.Write(resp)
				tconn := tls.Server(conn, tlsCfg)
				if err := tconn.Handshake(); err != nil {
					return
				}
				conn = tconn
			}
		case appBindRequest:
			dn := string(op.Children[1].Data)
			pw := string(op.Children[2].Data)
			code := int64(49) // invalidCredentials
			if (dn == "svc@corp.local" && pw == "svc-pass") || (dn == "CN=Ivan,DC=corp,DC=local" && pw == "user-pass") {
				code = 0
			}
			resp := encodeSequence(tagSequence,
				encodeInt(tagInteger, id),
				encodeSequence(appBindResponse,
					encodeInt(tagEnumerated, code),
					encodeString(""),
					encodeString(""),
				),
			)
			_, _ = conn.Write(resp)
		case appSearchRequest:
			entry := encodeSequence(tagSequence,
				encodeInt(tagInteger, id),
				encodeSequence(appSearchResultEntry,
					encodeString("CN=Ivan,DC=corp,DC=local"),
					encodeSequence(tagSequence,
						encodeSequence(tagSequence, encodeString("sAMAccountName"), encodeSequence(tagSet, encodeString("i.petrov"))),
						encodeSequence(tagSequence, encodeString("displayName"), encodeSequence(tagSet, encodeString("Ivan Petrov"))),
						encodeSequence(tagSequence, encodeString("objectGUID"), encodeSequence(tagSet, encodeTag(tagOctetString, guid))),
						encodeSequence(tagSequence, encodeString("memberOf"), encodeSequence(tagSet, encodeString("CN=OMB-Operators,DC=corp,DC=local"))),
					),
				),
			)
			done := encodeSequence(tagSequence,
				encodeInt(tagInteger, id),
				encodeSequence(appSearchResultDone,
					encodeInt(tagEnumerated, 0),
					encodeString(""),
					encodeString(""),
				),
			)
			_, _ = conn.Write(entry)
			_, _ = conn.Write(done)
		case appUnbindRequest:
			return
		}
	}
}

func TestLDAPClientLDAPSAndStartTLS(t *testing.T) {
	cert, caPEM := generateTestTLS(t)
	guid := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}

	for _, tc := range []struct {
		name     string
		startTLS bool
		scheme   string
		mode     string
	}{
		{"LDAPS", false, "ldaps://", "ldaps"},
		{"StartTLS", true, "ldap://", "starttls"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr, stop := startFakeLDAPServer(t, tc.startTLS, cert, guid)
			defer stop()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			c, err := Dial(ctx, DialOptions{
				ServerURL: tc.scheme + addr,
				TLSMode:   tc.mode,
				CAPEM:     caPEM,
				Timeout:   3 * time.Second,
			})
			if err != nil {
				t.Fatalf("Dial: %v", err)
			}
			defer c.Close()

			if err := c.Bind(ctx, "svc@corp.local", "wrong"); err == nil {
				t.Fatal("expected error on wrong password")
			}
			if err := c.Bind(ctx, "svc@corp.local", "svc-pass"); err != nil {
				t.Fatalf("Bind: %v", err)
			}

			entries, err := c.Search(ctx, "DC=corp,DC=local", ScopeWholeSubtree,
				"(&(objectClass=user)(!(userAccountControl:1.2.840.113556.1.4.803:=2))(sAMAccountName="+EscapeFilter("i.petrov")+"))",
				[]string{"sAMAccountName", "displayName", "objectGUID", "memberOf"}, 2)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(entries) != 1 {
				t.Fatalf("entries = %d, want 1", len(entries))
			}
			if got := entries[0].FirstString("sAMAccountName"); got != "i.petrov" {
				t.Fatalf("sAMAccountName = %q, want i.petrov", got)
			}
			if !bytes.Equal(entries[0].FirstBytes("objectGUID"), guid) {
				t.Fatalf("objectGUID mismatch")
			}
		})
	}
}

func TestGUIDAndFilterEscape(t *testing.T) {
	raw := []byte{0x78, 0x56, 0x34, 0x12, 0xbc, 0x9a, 0xf0, 0xde, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	formatted, err := FormatADGUID(raw)
	if err != nil {
		t.Fatal(err)
	}
	if formatted != "12345678-9abc-def0-1122-334455667788" {
		t.Fatalf("FormatADGUID = %q", formatted)
	}
	parsed, err := ParseADGUID(formatted)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(parsed, raw) {
		t.Fatalf("ParseADGUID roundtrip mismatch")
	}
	if got := EscapeFilter("a*(b)\\c\x00"); got != `a\2a\28b\29\5cc\00` {
		t.Fatalf("EscapeFilter = %q", got)
	}
}
