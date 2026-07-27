package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP (RFC 6238) para o login out-of-band do admin. Implementação stdlib,
// validada contra os vetores oficiais do RFC — evita dependência nova num
// caminho de auth crítico. Parâmetros compatíveis com Google Authenticator:
// SHA1, 6 dígitos, período de 30s.

const (
	totpDigits = 6
	totpPeriod = 30 // segundos
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// decodeSecret normaliza (remove espaços, uppercase) e decodifica o segredo
// base32.
func decodeSecret(secret string) ([]byte, error) {
	s := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	if s == "" {
		return nil, fmt.Errorf("segredo vazio")
	}
	return b32.DecodeString(s)
}

// generateTOTP calcula o código para o instante `at`.
func generateTOTP(secret string, at time.Time, digits int) (string, error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return "", err
	}
	return codeForCounter(key, uint64(at.Unix()/totpPeriod), digits), nil
}

// codeForCounter é o núcleo HOTP (RFC 4226): HMAC-SHA1 sobre o contador +
// truncamento dinâmico.
func codeForCounter(key []byte, counter uint64, digits int) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	val := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])

	mod := uint32(1)
	for i := 0; i < digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, val%mod)
}

// ValidateTOTP verifica `code` contra `secret` no instante `now`, tolerando
// ±skew passos de 30s (drift de relógio). Devolve o passo de tempo casado (para
// anti-replay) e ok. Comparação em tempo constante. Secret vazio nunca valida.
func ValidateTOTP(secret, code string, now time.Time, skew int) (int64, bool) {
	key, err := decodeSecret(secret)
	if err != nil {
		return 0, false
	}
	code = strings.TrimSpace(code)
	base := now.Unix() / totpPeriod
	for i := -skew; i <= skew; i++ {
		step := base + int64(i)
		want := codeForCounter(key, uint64(step), totpDigits)
		if hmac.Equal([]byte(want), []byte(code)) {
			return step, true
		}
	}
	return 0, false
}

// GenerateTOTPSecret cria um segredo aleatório de 160 bits (20 bytes) em base32.
func GenerateTOTPSecret() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return b32.EncodeToString(buf), nil
}

// TOTPProvisioningURI monta o otpauth:// que o app autenticador importa.
func TOTPProvisioningURI(secret, account, issuer string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprintf("%d", totpDigits))
	q.Set("period", fmt.Sprintf("%d", totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}
