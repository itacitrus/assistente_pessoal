package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"

	"rsc.io/qr"
)

// qrScale é o lado, em pixels, de cada módulo do QR. 8px dá uma imagem
// confortável de escanear na tela sem ficar gigante.
const qrScale = 8

// qrQuietZone é a borda branca (em módulos) exigida pela especificação do QR
// para leitura confiável.
const qrQuietZone = 4

// qrPNGDataURI renderiza a string de pareamento do WhatsApp (evt.Code) num
// data-URI PNG pronto para <img src=...>. Usa rsc.io/qr (já dependência
// transitiva) — nível L basta e maximiza a densidade útil.
func qrPNGDataURI(code string) (string, error) {
	if code == "" {
		return "", fmt.Errorf("qr: string vazia")
	}
	c, err := qr.Encode(code, qr.L)
	if err != nil {
		return "", fmt.Errorf("qr encode: %w", err)
	}

	dim := (c.Size + 2*qrQuietZone) * qrScale
	img := image.NewGray(image.Rect(0, 0, dim, dim))
	// Fundo branco.
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	// Módulos pretos, deslocados pela quiet zone e escalados.
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if !c.Black(x, y) {
				continue
			}
			px0 := (x + qrQuietZone) * qrScale
			py0 := (y + qrQuietZone) * qrScale
			for dy := 0; dy < qrScale; dy++ {
				for dx := 0; dx < qrScale; dx++ {
					img.SetGray(px0+dx, py0+dy, color.Gray{Y: 0x00})
				}
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", fmt.Errorf("png encode: %w", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
