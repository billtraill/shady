package encode

import (
	"bytes"
	"fmt"
	"image"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"time"
)

type PNGFormat struct{}

func (f PNGFormat) Extensions() []string {
	return []string{"png"}
}

func (f PNGFormat) Encode(w io.Writer, img image.Image) error {
	return png.Encode(w, img)
}

func (f PNGFormat) EncodeAnimation(w io.Writer, stream <-chan image.Image, interval time.Duration) error {
	for img := range stream {
		if err := f.Encode(w, img); err != nil {
			return err
		}
	}
	return nil
}

type JPGFormat struct{}

func (f JPGFormat) Extensions() []string {
	return []string{"jpg", "jpeg"}
}

func (f JPGFormat) Encode(w io.Writer, img image.Image) error {
	return jpeg.Encode(w, img, nil)
}

func (f JPGFormat) EncodeAnimation(w io.Writer, stream <-chan image.Image, interval time.Duration) error {
	for img := range stream {
		if err := f.Encode(w, img); err != nil {
			return err
		}
	}
	return nil
}

type RGB24Format struct{}

func (f RGB24Format) Extensions() []string {
	return []string{}
}

func (f RGB24Format) Encode(w io.Writer, img image.Image) error {
	bounds := img.Bounds()
	buf := make([]byte, bounds.Dx()*bounds.Dy()*3)
	if rgba, ok := img.(*image.RGBA); ok {
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				i := y*bounds.Dx() + x
				copy(buf[i*3:i*3+3], rgba.Pix[i*4:i*4+3])
			}
		}
	} else {
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				i := y*bounds.Dx() + x
				r, g, b, _ := img.At(x, y).RGBA()
				buf[i*3] = byte(r >> 8)
				buf[i*3+1] = byte(g >> 8)
				buf[i*3+2] = byte(b >> 8)
			}
		}
	}
	_, err := w.Write(buf)
	return err
}

func (f RGB24Format) EncodeAnimation(w io.Writer, stream <-chan image.Image, interval time.Duration) error {
	for img := range stream {
		if err := f.Encode(w, img); err != nil {
			return err
		}
	}
	return nil
}

type RGBA32Format struct{}

func (f RGBA32Format) Extensions() []string {
	return []string{}
}

func (f RGBA32Format) Encode(w io.Writer, img image.Image) error {
	var rgbaImg *image.RGBA
	if i, ok := img.(*image.RGBA); ok {
		rgbaImg = i
	} else {
		rgbaImg = image.NewRGBA(img.Bounds())
		draw.Draw(rgbaImg, img.Bounds(), img, image.Point{X: 0, Y: 0}, draw.Over)
	}
	_, err := w.Write(rgbaImg.Pix)
	return err
}

func (f RGBA32Format) EncodeAnimation(w io.Writer, stream <-chan image.Image, interval time.Duration) error {
	for img := range stream {
		if err := f.Encode(w, img); err != nil {
			return err
		}
	}
	return nil
}

type GIFFormat struct{}

func (f GIFFormat) Extensions() []string {
	return []string{"gif"}
}

func (f GIFFormat) Encode(w io.Writer, img image.Image) error {
	// Forward to the code stream encoder for easy code reuse.
	stream := make(chan image.Image, 1)
	stream <- img
	close(stream)
	return f.EncodeAnimation(w, stream, 0)
}

func (f GIFFormat) EncodeAnimation(w io.Writer, stream <-chan image.Image, interval time.Duration) error {
	gifImg := &gif.GIF{
		Image:           []*image.Paletted{},
		Delay:           []int{},
		LoopCount:       0,
		Disposal:        []byte{},
		BackgroundIndex: 0,
	}
	for img := range stream {
		frame := image.NewPaletted(img.Bounds(), palette.Plan9)
		draw.Draw(frame, img.Bounds(), img, image.Point{X: 0, Y: 0}, draw.Over)
		gifImg.Image = append(gifImg.Image, frame)
		gifImg.Delay = append(gifImg.Delay, int(interval/(time.Second/100)))
		gifImg.Disposal = append(gifImg.Disposal, gif.DisposalBackground)
	}
	return gif.EncodeAll(w, gifImg)
}

type AnsiDisplay struct {
	initDone bool
}

func (f AnsiDisplay) Extensions() []string {
	return []string{}
}

func (f AnsiDisplay) Encode(w io.Writer, img image.Image) error {
	// Forward to the code stream encoder for easy code reuse.
	stream := make(chan image.Image, 1)
	stream <- img
	close(stream)
	return f.EncodeAnimation(w, stream, 0)
}

func (f *AnsiDisplay) EncodeAnimation(w io.Writer, stream <-chan image.Image, interval time.Duration) error {
	// This implementation is taken from Ledcat:
	// https://github.com/billtraill/ledcat

	lastFrame := time.Now()
	for img := range stream {
		width, height := img.Bounds().Dx(), img.Bounds().Dy()
		// A buffer is used so frames can be written in one go, significantly
		// improving performance.
		var buf bytes.Buffer
		if !f.initDone {
			// Clear the screen and any previous frame with it.
			fmt.Fprintf(&buf, "\x1b[3J\x1b[H\x1b[2J")
			f.initDone = true
		} else {
			// Move the cursor to the top-left of the screen.
			fmt.Fprintf(&buf, "\x1b[1;1H")
		}

		// Two pixels are rendered at once using the Upper Half Block
		// character. The top half is colored with the foreground color while
		// the lower half uses the background. This neat trick allows us to
		// render square pixels with a higher density than combining two
		// rectangular characters.
		for y := 0; y < height/2+(height&1); y++ {
			for x := 0; x < width; x++ {
				// Set the foreground color.
				r, g, b, _ := img.At(x, y*2).RGBA()
				fmt.Fprintf(&buf, "\x1b[38;2;%d;%d;%dm", r/256, g/256, b/256)
				// Set the background color.
				if y*2+1 < img.Bounds().Dy() {
					r, g, b, _ := img.At(x, y*2+1).RGBA()
					fmt.Fprintf(&buf, "\x1b[48;2;%d;%d;%dm", r/256, g/256, b/256)
				} else {
					fmt.Fprintf(&buf, "\x1b[48;2;0m")
				}
				fmt.Fprintf(&buf, "\u2580")
			}
			// Reset to the default background color and jump to the next line.
			fmt.Fprintf(&buf, "\x1b[0m\n")
		}
		if _, err := io.Copy(w, &buf); err != nil {
			return err
		}

		time.Sleep(interval - time.Since(lastFrame))
		lastFrame = time.Now()
	}
	return nil
}
