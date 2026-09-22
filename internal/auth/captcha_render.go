package auth

import (
	"bytes"
	"crypto/rand"
	"image"
	"image/color"
	"image/png"
	"math"
	"math/big"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/f64"
	"golang.org/x/image/math/fixed"
)

// Rendered at 2x for sharp display at 120×38 CSS pixels.
const (
	captchaW     = 240
	captchaH     = 76
	captchaGlyph = 44.0
)

// Ink matches the UI's primary text colour; the page inverts it in dark mode.
var captchaInk = color.NRGBA{R: 0x1d, G: 0x1d, B: 0x1f, A: 0xff}

var captchaFont *opentype.Font

func init() {
	f, err := opentype.Parse(gomonobold.TTF)
	if err != nil {
		panic("captcha font: " + err.Error())
	}
	captchaFont = f
}

func renderCaptcha(text string) ([]byte, error) {
	face, err := opentype.NewFace(captchaFont, &opentype.FaceOptions{Size: captchaGlyph, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, err
	}
	defer func() { _ = face.Close() }()

	canvas := image.NewNRGBA(image.Rect(0, 0, captchaW, captchaH))
	cell := float64(captchaW-24) / float64(len(text))
	for i, r := range text {
		glyph := image.NewNRGBA(image.Rect(0, 0, 56, 64))
		d := font.Drawer{Dst: glyph, Src: image.NewUniform(captchaInk), Face: face, Dot: fixed.P(14, 50)}
		d.DrawString(string(r))

		angle := (randFloat()*2 - 1) * 0.35 // about ±20°
		cx := 12 + cell*float64(i) + cell/2 + (randFloat()*2-1)*4
		cy := float64(captchaH)/2 + (randFloat()*2-1)*6
		sin, cos := math.Sin(angle), math.Cos(angle)
		// Rotate about the glyph centre (28,32), then move it to (cx,cy).
		m := f64.Aff3{cos, -sin, cx - 28*cos + 32*sin, sin, cos, cy - 28*sin - 32*cos}
		draw.BiLinear.Transform(canvas, m, glyph, glyph.Bounds(), draw.Over, nil)
	}
	for range 2 {
		strike(canvas)
	}
	speckle(canvas, 90)

	var buf bytes.Buffer
	if err := png.Encode(&buf, canvas); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// strike draws a thin sine curve across the text.
func strike(img *image.NRGBA) {
	amp := 6 + randFloat()*10
	period := 90 + randFloat()*80
	phase := randFloat() * 2 * math.Pi
	base := float64(captchaH)*0.35 + randFloat()*float64(captchaH)*0.3
	ink := captchaInk
	ink.A = 0xb0
	for x := 0; x < captchaW; x++ {
		y := base + amp*math.Sin(float64(x)/period*2*math.Pi+phase)
		for dy := -1; dy <= 1; dy++ {
			blend(img, x, int(y)+dy, ink, 1-math.Abs(float64(dy))*0.5)
		}
	}
}

var speckleAlpha = []uint8{40, 60, 80, 100, 120}

func speckle(img *image.NRGBA, n int) {
	ink := captchaInk
	for range n {
		x, y := randInt(captchaW), randInt(captchaH)
		ink.A = speckleAlpha[randInt(len(speckleAlpha))]
		blend(img, x, y, ink, 1)
		blend(img, x+1, y, ink, 1)
	}
}

func blend(img *image.NRGBA, x, y int, c color.NRGBA, strength float64) {
	if !(image.Point{X: x, Y: y}.In(img.Rect)) {
		return
	}
	a := float64(c.A) / 255 * strength
	cur := img.NRGBAAt(x, y)
	outA := a + float64(cur.A)/255*(1-a)
	if outA == 0 {
		return
	}
	mix := func(src, dst uint8) uint8 {
		return uint8((float64(src)*a + float64(dst)*float64(cur.A)/255*(1-a)) / outA)
	}
	img.SetNRGBA(x, y, color.NRGBA{R: mix(c.R, cur.R), G: mix(c.G, cur.G), B: mix(c.B, cur.B), A: uint8(outA * 255)})
}

func randInt(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(v.Int64())
}

func randFloat() float64 { return float64(randInt(1<<20)) / (1 << 20) }
