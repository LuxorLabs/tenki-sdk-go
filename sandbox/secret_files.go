package sandbox

import (
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	sandboxv1 "github.com/LuxorLabs/tenki-sdk-go/sandbox/internal/proto/tenki/sandbox/v1"
	"google.golang.org/protobuf/proto"
)

type RuntimeSecretReference = sandboxv1.RuntimeSecretReference
type RuntimeSecretFile = sandboxv1.RuntimeSecretFile
type RuntimeSecretFileSource = sandboxv1.RuntimeSecretFile_Source
type RuntimeSecretFileContent = sandboxv1.RuntimeSecretFile_Content
type RuntimeSecretFileRaw = sandboxv1.RuntimeSecretFile_Raw

func cloneSecretFiles(files []*RuntimeSecretFile) []*RuntimeSecretFile {
	out := make([]*RuntimeSecretFile, len(files))
	for i, file := range files {
		if file != nil {
			out[i] = proto.Clone(file).(*RuntimeSecretFile)
		}
	}
	return out
}

func validateSecretFiles(files []*RuntimeSecretFile, count int) bool {
	if len(files) > 32 {
		return false
	}
	for i, file := range files {
		target := file.GetPath()
		if !utf8.ValidString(target) || len(target) > 4096 || path.Clean(target) != target || strings.ContainsAny(target, "\x00\r\n\t") {
			return false
		}
		allowed := false
		for _, root := range []string{"/home/tenki/", "/workspace/", "/app/"} {
			allowed = allowed || (strings.HasPrefix(target, root) && len(target) > len(root))
		}
		if !allowed {
			return false
		}
		for j, other := range files {
			if i != j && (target == other.GetPath() || strings.HasPrefix(target, other.GetPath()+"/")) {
				return false
			}
		}
		var refs []*RuntimeSecretReference
		switch format := file.GetFormat().(type) {
		case *RuntimeSecretFileSource:
			if !validateSecretFiles([]*RuntimeSecretFile{{Path: format.Source, Format: &RuntimeSecretFileRaw{Raw: &RuntimeSecretReference{Name: "SOURCE"}}}}, 0) {
				return false
			}
		case *RuntimeSecretFileContent:
			names, ok := SecretFileNames(format.Content)
			if !ok {
				return false
			}
			for _, name := range names {
				refs = append(refs, &RuntimeSecretReference{Name: name})
			}
		case *RuntimeSecretFileRaw:
			refs = append(refs, format.Raw)
		default:
			return false
		}
		count += len(refs)
		for _, ref := range refs {
			if !templateSecretNamePattern.MatchString(ref.GetName()) {
				return false
			}
		}
	}
	return count <= 64
}

// WithSecretFiles adds unresolved contents or raw references to a launch.
func WithSecretFiles(files ...*RuntimeSecretFile) CreateOption {
	snapshot := cloneSecretFiles(files)
	return createOptionFunc(func(cfg *createConfig) { cfg.secretFiles = cloneSecretFiles(snapshot) })
}

// SecretFileNames discovers references without resolving any values.
func SecretFileNames(content string) ([]string, bool) {
	if len(content) > 262144 || !utf8.ValidString(content) {
		return nil, false
	}
	for _, c := range content {
		if c < 32 && c != '\n' && c != '\r' && c != '\t' {
			return nil, false
		}
	}
	names := []string{}
	seen := map[string]bool{}
	token := regexp.MustCompile(`^[A-Za-z0-9_-]*`)
	for offset := 0; offset < len(content); {
		pos := strings.Index(content[offset:], "secrets://")
		if pos < 0 {
			break
		}
		index := offset + pos
		offset = index + len("secrets://")
		if index > 0 && content[index-1] == '\\' {
			continue
		}
		name := token.FindString(content[offset:])
		if !templateSecretNamePattern.MatchString(name) {
			return nil, false
		}
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
		if len(names) > 64 {
			return nil, false
		}
		offset += len(name)
	}
	return names, true
}
