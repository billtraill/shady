package main

import (
	"context"
	"flag"
	"fmt"
	"image"
	"io"
	"io/ioutil"
	"os"
	"os/signal"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-gl/gl/v3.3-core/gl"
	"github.com/polyfloyd/shady"
	"github.com/polyfloyd/shady/encode"
)

func main() {
	formatNames := make([]string, 0, len(encode.Formats))
	for name := range encode.Formats {
		formatNames = append(formatNames, name)
	}

	inputFile := flag.String("i", "-", "The shader file to use. Will read from stdin by default")
	outputFile := flag.String("o", "-", "The file to write the rendered image to")
	geometry := flag.String("g", "env", "The geometry of the rendered image in WIDTHxHEIGHT format. If \"env\", look for the LEDCAT_GEOMETRY variable")
	envName := flag.String("env", "", "The environment (aka website) to simulate. Valid values are \"glslsandbox\", \"shadertoy\" or \"\" to autodetect")
	outputFormat := flag.String("ofmt", "", "The encoding format to use to output the image. Valid values are: "+strings.Join(formatNames, ", "))
	framerate := flag.Float64("framerate", 0, "Whether to animate using the specified number of frames per second")
	numFrames := flag.Uint("numframes", 0, "Limit the number of frames in the animation. No limit is set by default")
	duration := flag.Uint("duration", 0, "Limit the animation to the specified number of seconds. No limit is set by default")
	verbose := flag.Bool("v", false, "Show verbose output about rendering")
	flag.Parse()

	if *duration != 0 && *numFrames != 0 {
		fmt.Fprintf(os.Stderr, "-duration and -numframes are mutually exclusive\n")
		os.Exit(1)
	}
	var animateNumFrames uint
	if *numFrames != 0 {
		if *framerate == 0 {
			fmt.Fprintf(os.Stderr, "-numframes is set while -framerate is not set\n")
			os.Exit(1)
		}
		animateNumFrames = *numFrames
	}
	if *duration != 0 {
		if *framerate == 0 {
			fmt.Fprintf(os.Stderr, "-duration is set while -framerate is not set\n")
			os.Exit(1)
		}
		animateNumFrames = uint(float64(*duration) * *framerate)
	}

	// Figure out the dimensions of the display.
	width, height, err := parseGeometry(*geometry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	var format encode.Format
	var ok bool
	if *outputFormat == "" {
		if format, ok = encode.DetectFormat(*outputFile); !ok {
			fmt.Fprintf(os.Stderr, "Unable to detect output format. Please set the -ofmt flag\n")
			os.Exit(1)
		}
	} else if format, ok = encode.Formats[*outputFormat]; !ok {
		fmt.Fprintf(os.Stderr, "Unknown output format: %q", *outputFile)
		os.Exit(1)
	}

	// Load the shader.
	shaderSourceFile, err := openReader(*inputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer shaderSourceFile.Close()
	shaderSource, err := ioutil.ReadAll(shaderSourceFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	// Lock this goroutine to the current thread. This is required because
	// OpenGL contexts are bounds to threads.
	runtime.LockOSThread()

	var env glsl.Environment
	switch *envName {
	case "glslsandbox":
		env = GLSLSandbox{}
	case "shadertoy":
		env = ShaderToy{}
	case "":
		var ok bool
		env, ok = DetectEnvironment(string(shaderSource))
		if !ok {
			fmt.Fprintf(os.Stderr, "Unable to detect the environment to use. Please set it using -env")
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown environment: %q", *envName)
	}

	// Compile the shader.
	sources := map[uint32][]string{
		gl.FRAGMENT_SHADER: {string(shaderSource)},
	}
	sh, err := glsl.NewShader(width, height, sources, env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer sh.Close()

	// Open the output.
	outWriter, err := openWriter(*outputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer outWriter.Close()

	if *framerate <= 0 {
		img := sh.Image()
		// We're not dealing with an animation, just export a single image.
		if err := format.Encode(outWriter, img); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		return
	}

	ctx, cancel := context.WithCancel(context.Background())

	interval := time.Duration(float64(time.Second) / *framerate)
	imgStream := make(chan image.Image, int(*framerate)+1)
	counterStream := make(chan image.Image)
	var waitgroup sync.WaitGroup
	waitgroup.Add(2)
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt)
		<-sig
		signal.Stop(sig)
		cancel()
	}()
	go func() {
		if err := format.EncodeAnimation(outWriter, counterStream, interval); err != nil {
			fmt.Fprintf(os.Stderr, "Error animating: %v", err)
			cancel()
			go func() {
				// Prevent deadlocking the counter routine.
				for range counterStream {
				}
			}()
		}
		waitgroup.Done()
	}()
	go func() {
		defer close(counterStream)
		lastFrame := time.Now()
		var frame uint
		for img := range imgStream {
			renderTime := time.Since(lastFrame)
			fps := 1.0 / (float64(renderTime) / float64(time.Second))
			speed := float64(interval) / float64(renderTime)
			lastFrame = time.Now()
			frame++

			if *verbose {
				var frameTarget string
				if animateNumFrames == 0 {
					frameTarget = "∞"
				} else {
					frameTarget = fmt.Sprintf("%d", animateNumFrames)
				}
				fmt.Fprintf(os.Stderr, "\rfps=%.2f frames=%d/%s speed=%.2f", fps, frame, frameTarget, speed)
			}

			counterStream <- img
			if frame == animateNumFrames {
				cancel()
				break
			}
		}
		if *verbose {
			fmt.Fprintf(os.Stderr, "\n")
		}
		waitgroup.Done()
	}()
	sh.Animate(ctx, interval, imgStream)
	close(imgStream)
	waitgroup.Wait()
}

func parseGeometry(geom string) (uint, uint, error) {
	if geom == "env" {
		geom = os.Getenv("LEDCAT_GEOMETRY")
		if geom == "" {
			return 0, 0, fmt.Errorf("LEDCAT_GEOMETRY is empty while instructed to load the display geometry from the environment")
		}
	}

	re := regexp.MustCompile("^(\\d+)x(\\d+)$")
	matches := re.FindStringSubmatch(geom)
	if matches == nil {
		return 0, 0, fmt.Errorf("invalid geometry: %q", geom)
	}
	w, _ := strconv.ParseUint(matches[1], 10, 32)
	h, _ := strconv.ParseUint(matches[2], 10, 32)
	if w == 0 || h == 0 {
		return 0, 0, fmt.Errorf("no geometry dimension can be 0, got (%d, %d)", w, h)
	}
	return uint(w), uint(h), nil
}

func openReader(filename string) (io.ReadCloser, error) {
	if filename == "-" {
		return ioutil.NopCloser(os.Stdin), nil
	}
	return os.Open(filename)
}

func openWriter(filename string) (io.WriteCloser, error) {
	if filename == "-" {
		return nopCloseWriter{Writer: os.Stdout}, nil
	}
	return os.Create(filename)
}

type nopCloseWriter struct {
	io.Writer
}

func (nopCloseWriter) Close() error {
	return nil
}