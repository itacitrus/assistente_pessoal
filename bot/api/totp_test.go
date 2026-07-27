package api

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

// rfcSecret é o seed ASCII "12345678901234567890" do RFC 6238 (SHA1), em base32.
func rfcSecret(t *testing.T) string {
	t.Helper()
	return base32.StdEncoding.WithPadding(base32.NoPadding).
		EncodeToString([]byte("12345678901234567890"))
}

// Vetores oficiais do RFC 6238 (SHA1), truncados para 6 dígitos (Google
// Authenticator). A tabela do RFC é 8 dígitos; os 6 baixos batem com estes.
func TestValidateTOTP_RFCVectors(t *testing.T) {
	secret := rfcSecret(t)
	cases := []struct {
		unix int64
		code string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
	}
	for _, c := range cases {
		now := time.Unix(c.unix, 0).UTC()
		step, ok := ValidateTOTP(secret, c.code, now, 0)
		if !ok {
			t.Fatalf("T=%d code=%s: esperava válido", c.unix, c.code)
		}
		if step != c.unix/30 {
			t.Fatalf("T=%d: step=%d, esperava %d", c.unix, step, c.unix/30)
		}
	}
}

func TestValidateTOTP_CodigoErrado(t *testing.T) {
	secret := rfcSecret(t)
	if _, ok := ValidateTOTP(secret, "000000", time.Unix(59, 0), 1); ok {
		t.Fatal("código errado não deveria validar")
	}
}

func TestValidateTOTP_Skew(t *testing.T) {
	secret := rfcSecret(t)
	// Código do passo anterior (T=29, step 0) deve valer em T=59 (step 1) com skew=1.
	prev := time.Unix(29, 0).UTC()
	codePrev, _ := generateTOTP(secret, prev, 6)
	now := time.Unix(59, 0).UTC()
	if _, ok := ValidateTOTP(secret, codePrev, now, 1); !ok {
		t.Fatal("código do passo anterior deveria valer com skew=1")
	}
	if _, ok := ValidateTOTP(secret, codePrev, now, 0); ok {
		t.Fatal("com skew=0 o código do passo anterior NÃO deveria valer")
	}
}

func TestValidateTOTP_SecretVazioNuncaValida(t *testing.T) {
	if _, ok := ValidateTOTP("", "287082", time.Unix(59, 0), 1); ok {
		t.Fatal("secret vazio nunca deve validar")
	}
}

func TestGenerateTOTPSecret_Base32Decodavel(t *testing.T) {
	s, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
	if err != nil {
		t.Fatalf("secret não decodifica base32: %v", err)
	}
	if len(raw) != 20 {
		t.Fatalf("secret com %d bytes, esperava 20", len(raw))
	}
	// Dois segredos consecutivos devem diferir (aleatoriedade).
	s2, _ := GenerateTOTPSecret()
	if s == s2 {
		t.Fatal("dois segredos gerados iguais — não é aleatório")
	}
}

func TestTOTPProvisioningURI_BemFormada(t *testing.T) {
	uri := TOTPProvisioningURI("JBSWY3DPEHPK3PXP", "admin@zello", "Zello")
	if !strings.HasPrefix(uri, "otpauth://totp/") {
		t.Fatalf("URI não começa com otpauth://totp/: %q", uri)
	}
	for _, want := range []string{"secret=JBSWY3DPEHPK3PXP", "issuer=Zello"} {
		if !strings.Contains(uri, want) {
			t.Fatalf("URI %q não contém %q", uri, want)
		}
	}
}
