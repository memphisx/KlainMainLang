package llvm

import (
	_ "embed"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// assets.go — TDD-00142 Stage 7: compile-time embedding of a built SPA/SSG
// directory into the executable (klain:assets `embedDir` / `Webview({ serve })`).
// The emitter records the compile-time-literal directory path; EmbeddedBlobs()
// (read by the CLI driver after EmitProgram, like LinkLibs()/EmbeddedCSources())
// walks the dir and packs it into a self-describing blob. The driver writes the
// blob as a sidecar `.bin` and a generated `.s` that `.incbin`s it under a
// per-GOOS symbol; the runtime static server (embedassetssrc/embed_assets.c)
// serves it.

//go:embed embedassetssrc/embed_assets.c
var embedAssetsSource string

// EmbedAssetsSource returns the embedded static-server C runtime.
func EmbedAssetsSource() string { return embedAssetsSource }

// EmbeddedBlob is one packed embedded directory: the symbol its bytes are linked
// under (IR references `@<Symbol>`) and the packed image.
type EmbeddedBlob struct {
	Symbol string
	Blob   []byte
}

// requireEmbed records that the program embeds dir (a compile-time-literal path),
// returning the IR symbol name for its blob. Deduped by path, so `serve` and an
// explicit embedDir of the same directory share one blob.
func (e *Emitter) requireEmbed(dir string) string {
	if e.embeddedDirs == nil {
		e.embeddedDirs = map[string]string{}
	}
	if sym, ok := e.embeddedDirs[dir]; ok {
		return sym
	}
	sym := fmt.Sprintf("kml_embed_%d", len(e.embeddedDirs))
	e.embeddedDirs[dir] = sym
	e.usedEmbeddedAssets = true
	return sym
}

// UsesEmbeddedAssets reports whether the program embedded any directory (gates
// the embed_assets.c runtime link).
func (e *Emitter) UsesEmbeddedAssets() bool { return e.usedEmbeddedAssets }

// EmbeddedBlobs walks each recorded directory and packs it, returning one blob
// per embedded dir. Read by the driver after EmitProgram. Deterministic order
// (by symbol) so the .ll's symbol references and the emitted blobs line up.
func (e *Emitter) EmbeddedBlobs() ([]EmbeddedBlob, error) {
	dirs := make([]struct{ dir, sym string }, 0, len(e.embeddedDirs))
	for dir, sym := range e.embeddedDirs {
		dirs = append(dirs, struct{ dir, sym string }{dir, sym})
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].sym < dirs[j].sym })
	out := make([]EmbeddedBlob, 0, len(dirs))
	for _, d := range dirs {
		blob, err := packDir(d.dir)
		if err != nil {
			return nil, fmt.Errorf("embedDir(%q): %w", d.dir, err)
		}
		out = append(out, EmbeddedBlob{Symbol: d.sym, Blob: blob})
	}
	return out, nil
}

// ctypeEnum maps a file extension to the content-type enum embed_assets.c knows.
func ctypeEnum(path string) uint32 {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".html", ".htm":
		return 1
	case ".js", ".mjs":
		return 2
	case ".css":
		return 3
	case ".json":
		return 4
	case ".svg":
		return 5
	case ".woff":
		return 6
	case ".woff2":
		return 7
	case ".png":
		return 8
	case ".jpg", ".jpeg":
		return 9
	case ".gif":
		return 10
	case ".wasm":
		return 11
	case ".txt":
		return 12
	case ".ico":
		return 13
	case ".map":
		return 14
	case ".webp":
		return 15
	default:
		return 0
	}
}

type packEntry struct {
	path  string // "/index.html", "/assets/x.js" — root-relative, leading slash
	data  []byte
	ctype uint32
}

// packDir walks root recursively and builds the packed blob (see the format in
// embed_assets.c): 16-byte header, sorted 24-byte entries, string table, data.
func packDir(root string) ([]byte, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory")
	}
	var entries []packEntry
	err = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		entries = append(entries, packEntry{
			path:  "/" + filepath.ToSlash(rel),
			data:  data,
			ctype: ctypeEnum(p),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("directory is empty")
	}
	// Sort by path for the runtime binary search.
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })

	const headerSize = 16
	const entrySize = 24
	strTableOff := headerSize + entrySize*len(entries)

	// Build the string table + data region, recording offsets.
	var strTable []byte
	var dataRegion []byte
	pathOffs := make([]int, len(entries))
	dataOffs := make([]int, len(entries))
	for i, en := range entries {
		pathOffs[i] = strTableOff + len(strTable)
		strTable = append(strTable, []byte(en.path)...)
		strTable = append(strTable, 0)
	}
	dataStart := strTableOff + len(strTable)
	for i, en := range entries {
		dataOffs[i] = dataStart + len(dataRegion)
		dataRegion = append(dataRegion, en.data...)
	}

	total := dataStart + len(dataRegion)
	blob := make([]byte, total)
	copy(blob[0:4], []byte("KMLA"))
	binary.LittleEndian.PutUint32(blob[4:8], 1) // version
	binary.LittleEndian.PutUint32(blob[8:12], uint32(len(entries)))
	// blob[12:16] reserved = 0
	for i, en := range entries {
		off := headerSize + entrySize*i
		binary.LittleEndian.PutUint32(blob[off+0:off+4], uint32(pathOffs[i]))
		binary.LittleEndian.PutUint32(blob[off+4:off+8], uint32(len(en.path)))
		binary.LittleEndian.PutUint32(blob[off+8:off+12], uint32(dataOffs[i]))
		binary.LittleEndian.PutUint32(blob[off+12:off+16], uint32(len(en.data)))
		binary.LittleEndian.PutUint32(blob[off+16:off+20], en.ctype)
		// reserved off+20:off+24 = 0
	}
	copy(blob[strTableOff:], strTable)
	copy(blob[dataStart:], dataRegion)
	return blob, nil
}

// EmbedBlobAsm generates the assembly stub that links a packed blob under the
// per-GOOS symbol for `sym` (IR references `@sym`; the Mach-O ABI adds the
// leading underscore, ELF does not). binPath is the absolute path of the
// sidecar .bin the driver wrote. goos is the host GOOS.
func EmbedBlobAsm(sym, binPath, goos string) string {
	if goos == "darwin" {
		return fmt.Sprintf(".section __DATA,__const\n.globl _%s\n.p2align 3\n_%s:\n.incbin %q\n", sym, sym, binPath)
	}
	return fmt.Sprintf(".section .rodata\n.globl %s\n.p2align 3\n%s:\n.incbin %q\n", sym, sym, binPath)
}
