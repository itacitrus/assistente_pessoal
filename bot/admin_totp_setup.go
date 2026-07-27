package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/giovannirambo/assistente_pessoal/bot/api"
	"rsc.io/qr"
)

// adminTOTPSetup gera um segredo TOTP e imprime tudo que o operador precisa para
// habilitar o login out-of-band do admin: o valor para o .env, o otpauth:// URI
// e um QR em ASCII para escanear no app autenticador. Rodado uma vez, pelo
// operador — o segredo nunca passa pelo assistente.
//
//	bot admin-totp-setup [conta]
func adminTOTPSetup() {
	account := "admin"
	if len(os.Args) > 2 && strings.TrimSpace(os.Args[2]) != "" {
		account = strings.TrimSpace(os.Args[2])
	}

	secret, err := api.GenerateTOTPSecret()
	if err != nil {
		log.Fatalf("gerar segredo: %v", err)
	}
	uri := api.TOTPProvisioningURI(secret, account, "Zello")

	fmt.Println("=== Setup do login TOTP do admin ===")
	fmt.Println()
	fmt.Println("1) Escaneie este QR no Google Authenticator / Authy:")
	fmt.Println()
	if err := printTerminalQR(uri); err != nil {
		fmt.Printf("   (não consegui renderizar o QR: %v)\n", err)
	}
	fmt.Println()
	fmt.Println("   Ou digite o segredo manualmente no app:")
	fmt.Printf("   %s\n", spaceEvery(secret, 4))
	fmt.Println()
	fmt.Println("2) Adicione ao .env de produção e reinicie o bot:")
	fmt.Printf("   ADMIN_TOTP_SECRET=%s\n", secret)
	fmt.Println()
	fmt.Println("   otpauth URI (backup):")
	fmt.Printf("   %s\n", uri)
	fmt.Println()
	fmt.Println("Feito isso, o login do admin (telefone + código de 6 dígitos)")
	fmt.Println("funciona mesmo com o WhatsApp do bot fora do ar.")
}

// printTerminalQR renderiza o QR com dois módulos por caractere usando
// meio-blocos unicode — legível em qualquer terminal, sem dependência extra.
func printTerminalQR(text string) error {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return err
	}
	const quiet = 2
	size := code.Size + 2*quiet
	// Cada linha de caracteres cobre duas linhas de módulos (topo/baixo).
	for y := -quiet; y < code.Size+quiet; y += 2 {
		var b strings.Builder
		for x := -quiet; x < code.Size+quiet; x++ {
			top := code.Black(x, y)
			bottom := code.Black(x, y+1)
			switch {
			case top && bottom:
				b.WriteRune('█')
			case top && !bottom:
				b.WriteRune('▀')
			case !top && bottom:
				b.WriteRune('▄')
			default:
				b.WriteRune(' ')
			}
		}
		fmt.Println("   " + b.String())
	}
	_ = size
	return nil
}

// spaceEvery insere um espaço a cada n runas, para o segredo ficar legível.
func spaceEvery(s string, n int) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && i%n == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}
