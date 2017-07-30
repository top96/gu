package assets

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"sync"
	"text/template"

	"github.com/gu-io/gu/generators/data"
	"github.com/influx6/moz/gen"
)

var (
	bufferPool = sync.Pool{
		New: func() interface{} {
			return &bytes.Buffer{}
		},
	}
)

// StaticDirective defines a specific directive option which requires the content of
// the file be written into it's own single file as dictated by the DirName and FileName
// provided.
type StaticDirective struct {
	WriteInFile bool
	FileName    string
	DirName     string
}

// WriteDirective defines a type which defines a directive with details of the
// content to be written to and the original path and abspath of it's origin.
type WriteDirective struct {
	OriginPath    string
	OriginAbsPath string
	Writer        io.WriterTo
	Static        *StaticDirective
}

// Read will copy directives writer into a content buffer and returns the giving string
// representation of that data, content will be gzipped.
func (directive WriteDirective) Read() (string, error) {
	buffer, ok := bufferPool.Get().(*bytes.Buffer)
	if !ok {
		return "", errors.New("BufferPool behaving incorrectly")
	}

	defer buffer.Reset()
	defer bufferPool.Put(buffer)

	// hxs := hexwriter.New(gzip.NewWriter(buffer))
	hxs := gzip.NewWriter(buffer)

	if _, err := directive.Writer.WriteTo(hxs); err != nil && err != io.EOF {
		return fmt.Sprintf("%q", buffer.Bytes()), err
	}

	return fmt.Sprintf("%q", buffer.Bytes()), nil
}

// Packer exposes a interface which exposes methods for validating the type of files
// it supports and a method to appropriately pack the FileStatments as desired
// into the given endpoint directory.
type Packer interface {
	Pack(files []FileStatement, dir DirStatement) ([]WriteDirective, error)
}

// Webpack defines the core structure for handling bundling of different assets
// using registered packers.
type Webpack struct {
	defaultPacker Packer
	packers       map[string]Packer
}

// New returns a new instance of the Webpack.
func New(defaultPacker Packer) *Webpack {
	return &Webpack{
		defaultPacker: defaultPacker,
		packers:       make(map[string]Packer, 0),
	}
}

// Register adds the Packer to manage the building of giving exensions.
func (w *Webpack) Register(ext string, packer Packer) {
	w.packers[ext] = packer
}

// Build runs through the directory pull all files and runs them through the
// packers to service each files by extension and returns a slice of all
// WriteDirective for final processing.
func (w *Webpack) Build(dir string, doGoSources bool) (map[string][]WriteDirective, map[string][]WriteDirective, error) {
	statement, err := GetDirStatement(dir, doGoSources)
	if err != nil {
		return nil, nil, err
	}

	wd := make(map[string][]WriteDirective, 0)
	staticWd := make(map[string][]WriteDirective, 0)

	for ext, fileStatement := range statement.FilesByExt {
		packer, ok := w.packers[ext]
		if !ok && w.defaultPacker == nil {
			continue
		}

		var derr error
		var directives []WriteDirective

		if w.defaultPacker != nil && !ok {
			directives, derr = w.defaultPacker.Pack(fileStatement, statement)
		} else {
			directives, derr = packer.Pack(fileStatement, statement)
		}

		if derr != nil {
			return wd, staticWd, err
		}

		for _, directive := range directives {
			fileExt := getExtension(directive.OriginPath)

			if directive.Static != nil {
				if ext == fileExt {
					staticWd[ext] = append(staticWd[ext], directive)
					continue
				}

				staticWd[fileExt] = append(staticWd[ext], directive)
				continue
			}

			if ext == fileExt {
				wd[ext] = append(wd[ext], directive)
				continue
			}

			wd[fileExt] = append(wd[ext], directive)
		}
	}

	return wd, staticWd, nil
}

// Compile returns a io.WriterTo which contains a complete source of all assets
// generated and stored inside a io.WriteTo which will contain the go source excluding
// the package declaration so has to allow you write the contents into the package
// you wish.
func (w *Webpack) Compile(dir string, doGoSources bool) (io.WriterTo, map[string][]WriteDirective, error) {
	directives, statics, err := w.Build(dir, doGoSources)
	if err != nil {
		return nil, nil, err
	}

	content := gen.Block(
		gen.SourceTextWith(
			string(data.Must("scaffolds/pack-bundle-src.gen")),
			template.FuncMap{},
			struct {
				Dir        string
				Directives map[string][]WriteDirective
			}{
				Dir:        dir,
				Directives: directives,
			},
		),
	)

	return content, statics, nil
}