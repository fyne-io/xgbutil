/*
   Package xgraphics makes drawing graphics to windows a bit easier.
   It uses the method of paiting a background pixmap.

   This packages requires the freetype and graphics packages from Google.

   This package is probably incomplete. I admit that I designed it with
   my window manager as a use case.
*/
package xgraphics

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"

	"code.google.com/p/graphics-go/graphics"

	"code.google.com/p/freetype-go/freetype"
	"code.google.com/p/freetype-go/freetype/truetype"

	"code.google.com/p/jamslam-x-go-binding/xgb"

	"github.com/BurntSushi/xgbutil"
	"github.com/BurntSushi/xgbutil/ewmh"
	"github.com/BurntSushi/xgbutil/xwindow"
)

// DrawText takes an image and, using the freetype package, writes text in the
// position specified on to the image. A color.Color, a font size and a font  
// must also be specified.
// Finally, the (x, y) coordinate advanced by the text extents is returned.
func DrawText(img draw.Image, x int, y int, clr color.Color, fontSize float64,
	font *truetype.Font, text string) (int, int, error) {

	// Create a solid color image
	textClr := image.NewUniform(clr)

	// Set up the freetype context... mostly boiler plate
	c := ftContext(font, fontSize)
	c.SetClip(img.Bounds())
	c.SetDst(img)
	c.SetSrc(textClr)

	// Now let's actually draw the text...
	pt := freetype.Pt(x, y+c.FUnitToPixelRU(font.UnitsPerEm()))
	newpt, err := c.DrawString(text, pt)
	if err != nil {
		return 0, 0, err
	}

	// i think this is right...
	return int(newpt.X / 256), int(newpt.Y / 256), nil
}

// Returns the width and height extents of a string given a font.
// TODO: This does not currently account for multiple lines. It may never do so.
func TextMaxExtents(font *truetype.Font, fontSize float64,
	text string) (width int, height int, err error) {

	// We need a context to calculate the extents
	c := ftContext(font, fontSize)

	emSquarePix := c.FUnitToPixelRU(font.UnitsPerEm())
	return len(text) * emSquarePix, emSquarePix, nil
}

// ftContext does the boiler plate to create a freetype context
func ftContext(font *truetype.Font, fontSize float64) *freetype.Context {
	c := freetype.NewContext()
	c.SetDPI(72)
	c.SetFont(font)
	c.SetFontSize(fontSize)

	return c
}

// ParseFont reads a font file and creates a freetype.Font type
func ParseFont(fontBytes []byte) (*truetype.Font, error) {
	font, err := freetype.ParseFont(fontBytes)
	if err != nil {
		return nil, err
	}

	return font, nil
}

// CreateImageWindow automatically creates a window with the same size as
// the image given, positions it according to the x,y coordinates given,
// paints the image onto the background of the image, and returns the window
// id. It does *not* map the window for you though. You'll need to that
// with `X.Conn().MapWindow(window_id)`.
// XXX: This will likely change to include the window masks and vals as
// parameters.
func CreateImageWindow(xu *xgbutil.XUtil, img image.Image, x, y int) xgb.Id {
	win := xu.Conn().NewId()
	scrn := xu.Screen()
	width, height := GetDim(img)

	winMask := uint32(xgb.CWBackPixmap | xgb.CWOverrideRedirect)
	winVals := []uint32{xgb.BackPixmapParentRelative, 1}
	xu.Conn().CreateWindow(scrn.RootDepth, win, xu.RootWin(),
		int16(x), int16(y),
		uint16(width), uint16(height),
		0, xgb.WindowClassInputOutput, scrn.RootVisual,
		winMask, winVals)

	PaintImg(xu, win, img)

	return win
}

// PaintImg will slap the given image as a background pixmap into the given
// window.
func PaintImg(xu *xgbutil.XUtil, win xgb.Id, img image.Image) {
	pix := CreatePixmap(xu, img)
	xu.Conn().ChangeWindowAttributes(win, uint32(xgb.CWBackPixmap),
		[]uint32{uint32(pix)})
	xu.Conn().ClearArea(false, win, 0, 0, 0, 0)
	FreePixmap(xu, pix)
}

// CreatePixmap creates a pixmap from an image.
// Please remember to call FreePixmap when you're done!
// Note: This gets around the X maximum request size limitation by issuing
// multiple PutImage requests when the image data is too big.
func CreatePixmap(xu *xgbutil.XUtil, img image.Image) xgb.Id {
	width, height := GetDim(img)
	imgData := make([]byte, width*height*4)
	imgDataLen := len(imgData)
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			r, g, b, a := img.At(x, y).RGBA()
			i := 4 * (x + (y * width))
			imgData[i+0] = byte(b >> 8)
			imgData[i+1] = byte(g >> 8)
			imgData[i+2] = byte(r >> 8)
			imgData[i+3] = byte(a >> 8)
		}
	}

	pix := xu.Conn().NewId()
	xu.Conn().CreatePixmap(xu.Screen().RootDepth, pix,
		xgb.Drawable(xu.RootWin()), uint16(width), uint16(height))

	// This is where things get hairy. X's max request size is
	// (2^16) * 4, that is, the number of bytes specifiable in 4-byte
	// blocks by an unsigned 16 bit integer.
	// We check if the imgData is bigger than this max size minus 28 bytes
	// (accounting for the fixed size of a PutImage request). If it is,
	// we need to split up the data into chunks and send it that way.
	sends := imgDataLen/(xgbutil.MAX_REQ_SIZE-28) + 1
	rowsPer := (xgbutil.MAX_REQ_SIZE - 28) / (width * 4)

	// The idea here is to send "sends" number of PutImage requests with
	// "rowsPer" rows of the image each time. (If a single row of an image
	// exceeds the max request limit, we're in real trouble, but I don't
	// think that's possible anyway since it would also the size of a uint16.)
	ypos, startBytes, endBytes := 0, 0, 0
	var data []byte
	var h int // the height of each PutImage request
	for i := 0; i < sends; i++ {
		// The number of bytes we need to consume for 'rowsPer' rows is
		// the number of rows, times the width times 4. (Times 4 because the
		// depth of our image is 4: each pixel is represented by 4 bytes.)
		endBytes = startBytes + rowsPer*width*4

		// If this is the last send, it's quite likely that we don't
		// have enough bytes left to accomodate 'endBytes'
		if endBytes > len(imgData) {
			endBytes = len(imgData)
		}

		// Use slicing to get the bytes we want.
		data = imgData[startBytes:endBytes]

		// Calculate the height. It's usually just 'rowsPer', but if this
		// is the last send, we probably aren't sending 'rowsPer' rows.
		h = len(data) / 4 / width

		xu.Conn().PutImage(xgb.ImageFormatZPixmap, xgb.Drawable(pix), xu.GC(),
			uint16(width), uint16(h), 0, int16(ypos),
			0, 24, data)

		// Prep values for the next iteration
		startBytes = endBytes
		ypos += rowsPer
	}

	return pix
}

// FreePixmap frees the resources associated with pix.
func FreePixmap(xu *xgbutil.XUtil, pix xgb.Id) {
	xu.Conn().FreePixmap(pix)
}

// GetDim gets the width and height of an image
func GetDim(img image.Image) (int, int) {
	bounds := img.Bounds()
	return bounds.Max.X - bounds.Min.X, bounds.Max.Y - bounds.Min.Y
}

// LoadPngFromFile takes a file name for a png and loads it as an draw.Image.
func LoadPngFromFile(file string) (draw.Image, error) {
	srcReader, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer srcReader.Close()

	img, err := png.Decode(srcReader)
	if err != nil {
		return nil, err
	}

	return img.(draw.Image), nil
}

// LoadPngFromBytes takes a slice of raw bytes and loads it as a draw.Image.
func LoadPngFromBytes(bs []byte) (draw.Image, error) {
	img, err := png.Decode(bytes.NewReader(bs))
	if err != nil {
		return nil, err
	}

	return img.(draw.Image), nil
}

// BlendBg "blends" img with mask into a background with color clr with
// transparency, where alpha is a number 0-100 where 0 is completely
// transparent and 100 is completely opaque.
// It is very possible that I'm doing more than I need to here, but this
// was the only way I could get it to work.
func BlendBg(img image.Image, mask draw.Image, alpha int,
	clr color.RGBA) *image.RGBA {
	dest := image.NewRGBA(img.Bounds())
	draw.Draw(dest, dest.Bounds(), image.NewUniform(clr), image.ZP, draw.Src)
	Blend(dest, img, mask, alpha, 0, 0)
	return dest
}

// Blend "blends" img with mask into dest at position (x, y) with
// transparency alpha.
func Blend(dest draw.Image, img image.Image, mask draw.Image, alpha, x, y int) {
	transClr := uint8((float64(alpha) / 100.0) * 255.0)
	blendMask := image.NewUniform(color.Alpha{transClr})

	if mask != nil {
		draw.DrawMask(mask, mask.Bounds(), mask, image.ZP, blendMask, image.ZP,
			draw.Src)
	}

	width, height := GetDim(img)
	rect := image.Rect(x, y, width+x, height+y)
	if mask != nil {
		draw.DrawMask(dest, rect, img, image.ZP, mask, image.ZP, draw.Over)
	} else {
		draw.DrawMask(dest, rect, img, image.ZP, blendMask, image.ZP, draw.Over)
	}
}

// Scale is a simple wrapper around graphics.Scale. It will also scale a
// mask appropriately.
func Scale(img image.Image, width, height int) draw.Image {
	dimg := image.NewRGBA(image.Rect(0, 0, width, height))
	graphics.Scale(dimg, img)

	return dimg
}

// ColorImage replaces all colors in 'img' with 'clr', but does not change
// the alpha channel. (Useful for bitmap re-coloring.)
func ColorImage(img draw.Image, clr color.Color) {
	width, height := GetDim(img)
	r32, g32, b32, _ := clr.RGBA()
	r, g, b := uint8(r32), uint8(g32), uint8(b32)

	var a uint32
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			_, _, _, a = img.At(x, y).RGBA()
			if a > 0 { // only change visible colors
				img.Set(x, y, color.RGBA{r, g, b, uint8(a)})
			}
		}
	}
}

// FindBestIcon takes width/height dimensions and a slice of *ewmh.WmIcon
// and finds the best matching icon of the bunch. We always prefer bigger.
// If no icons are bigger than the preferred dimensions, use the biggest
// available. Otherwise, use the smallest icon that is greater than or equal
// to the preferred dimensions. The preferred dimensions is essentially
// what you'll likely scale the resulting icon to.
func FindBestIcon(width, height int, icons []ewmh.WmIcon) *ewmh.WmIcon {
	// nada nada limonada
	if len(icons) == 0 {
		return nil
	}

	parea := width * height // preferred size
	best := -1

	var bestArea, iconArea int

	for i, icon := range icons {
		// the first valid icon we've seen; use it!
		if best == -1 {
			best = i
			continue
		}

		// load areas for comparison
		bestArea = icons[best].Width * icons[best].Height
		iconArea = icon.Width * icon.Height

		// We don't always want to accept bigger icons if our best is
		// already bigger. But we always want something bigger if our best
		// is insufficient.
		if (iconArea >= parea && iconArea <= bestArea) ||
			(bestArea < parea && iconArea > bestArea) {
			best = i
		}
	}

	if best > -1 {
		return &icons[best]
	}
	return nil
}

// proportional takes a pair of dimensions and returns whether they are
// proportional or not.
// XXX: Not currently used.
func proportional(w1, h1, w2, h2 uint32) bool {
	fw1, fh1 := float64(w1), float64(h1)
	fw2, fh2 := float64(w2), float64(h2)

	return fw1/fh1 == fw2/fh2
}

// PixmapToImage takes a Pixmap ID and converts it to an image.
// Pixmap data is in BGR order. Ew.
func PixmapToImage(xu *xgbutil.XUtil, pix xgb.Id) (*image.RGBA, error) {
	geom, err := xwindow.RawGeometry(xu, pix)
	if err != nil {
		return nil, err
	}

	width, height := geom.Width(), geom.Height()
	data, err := xu.Conn().GetImage(xgb.ImageFormatZPixmap, xgb.Drawable(pix),
		0, 0, uint16(width), uint16(height), (1<<32)-1)
	if err != nil {
		return nil, err
	}

	buf := make([]color.RGBA, width*height)
	// bufa := make([]color.Alpha, width * height) 
	for i, j := 0, 0; i < len(data.Data); i, j = i+4, j+1 {
		blue := data.Data[i+0]
		green := data.Data[i+1]
		red := data.Data[i+2]
		// alpha := data.Data[i + 3] 

		buf[j] = color.RGBA{uint8(red), uint8(green), uint8(blue), 255}
		// bufa[j] = color.Alpha{uint8(alpha)} 
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	// mask := image.NewRGBA(image.Rect(0, 0, width, height)) 
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			img.SetRGBA(x, y, buf[x+y*width])
			// mask.Set(x, y, color.Alpha{uint8(128)}) 
		}
	}
	return img, nil
}

// BitmapToImage takes a Pixmap ID and converts it to an image.
func BitmapToImage(xu *xgbutil.XUtil, pix xgb.Id) (*image.RGBA, error) {
	geom, err := xwindow.RawGeometry(xu, pix)
	if err != nil {
		return nil, err
	}

	width, height := geom.Width(), geom.Height()
	data, err := xu.Conn().GetImage(xgb.ImageFormatXYPixmap, xgb.Drawable(pix),
		0, 0, uint16(width), uint16(height), (1<<32)-1)
	if err != nil {
		return nil, err
	}

	whiteOrBlack := func(b uint8) color.Alpha {
		if b&1 > 0 {
			return color.Alpha{255}
		}
		return color.Alpha{0}
	}

	// First load the bitmap into a buffer
	buf := make([]color.Alpha, width*height)
	var b uint8
	for i := 0; i < len(data.Data); i++ {
		b = data.Data[i]
		for k := 0; k < 8; k++ {
			buf[i*8+k] = whiteOrBlack(b)
			b >>= 1
		}
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			img.Set(x, y, buf[x+(y*width)])
		}
	}
	return img, nil
}

// EwmhIconToImage takes a ewmh.WmIcon and converts it to an image and
// an alpha mask. A ewmh.WmIcon is in ARGB order, and the image package wants
// things in RGBA order. (What makes things is worse is when it comes time
// to paint the image to the screen, X wants it in BGR order. *facepalm*.)
func EwmhIconToImage(icon *ewmh.WmIcon) (img *image.RGBA, mask *image.RGBA) {
	width, height := int(icon.Width), int(icon.Height)
	img = image.NewRGBA(image.Rect(0, 0, width, height))
	mask = image.NewRGBA(image.Rect(0, 0, width, height))

	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			argb := icon.Data[x+(y*width)]
			alpha := argb >> 24
			red := ((alpha << 24) ^ argb) >> 16
			green := (((alpha << 24) + (red << 16)) ^ argb) >> 8
			blue := (((alpha << 24) + (red << 16) + (green << 8)) ^ argb) >> 0

			c := color.RGBA{uint8(red), uint8(green), uint8(blue), 255}

			img.SetRGBA(x, y, c)
			mask.Set(x, y, color.Alpha{uint8(alpha)})
		}
	}

	return
}
