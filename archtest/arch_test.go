// Package archtest impõe a regra de dependência (gate G4) lendo os imports de
// todos os arquivos Go do repositório.
package archtest

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const modulePrefix = "github.com/MarceloVilela/microservices-go/"

// Módulos de serviço; platform e ms-queues têm regra própria.
var services = []string{"ms-ingest", "ms-worker", "ms-notification", "ms-api", "ms-queues"}

type file struct {
	module  string // primeiro diretório (ex.: ms-ingest)
	rel     string // caminho dentro do módulo
	imports []string
}

func load(t *testing.T) []file {
	t.Helper()
	root := filepath.Join("..")
	var out []file
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "specs", "vendor", "node_modules":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		parts := strings.SplitN(filepath.ToSlash(rel), "/", 2)
		if len(parts) < 2 {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		fl := file{module: parts[0], rel: parts[1]}
		for _, im := range f.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			fl.imports = append(fl.imports, p)
		}
		out = append(out, fl)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("nenhum arquivo Go encontrado: o teste não está olhando o repositório")
	}
	return out
}

// Serviços só dependem de platform, nunca uns dos outros.
func TestServicosNaoImportamOutrosServicos(t *testing.T) {
	for _, f := range load(t) {
		if !slices.Contains(services, f.module) {
			continue
		}
		for _, im := range f.imports {
			if !strings.HasPrefix(im, modulePrefix) {
				continue
			}
			target := strings.SplitN(strings.TrimPrefix(im, modulePrefix), "/", 2)[0]
			if target != f.module && target != "platform" {
				t.Errorf("%s/%s importa %s: serviços só podem importar platform", f.module, f.rel, im)
			}
		}
	}
}

func TestPlatformNaoImportaServicos(t *testing.T) {
	for _, f := range load(t) {
		if f.module != "platform" {
			continue
		}
		for _, im := range f.imports {
			if strings.HasPrefix(im, modulePrefix) && !strings.HasPrefix(im, modulePrefix+"platform") {
				t.Errorf("platform/%s importa %s", f.rel, im)
			}
		}
	}
}

func TestDomainNaoImportaAdapters(t *testing.T) {
	for _, f := range load(t) {
		if !strings.Contains(f.rel, "internal/domain/") {
			continue
		}
		for _, im := range f.imports {
			if strings.Contains(im, "/internal/adapters") {
				t.Errorf("%s/%s (domain) importa %s", f.module, f.rel, im)
			}
		}
	}
}

// Dono único da topologia: só ms-queues e platform/amqpx podem declarar.
func TestSoTopologyInitDeclara(t *testing.T) {
	forbidden := []string{".ExchangeDeclare(", ".QueueDeclare(", ".QueueBind("}
	err := filepath.WalkDir(filepath.Join(".."), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "specs", "archtest":
				return fs.SkipDir
			}
			return nil
		}
		slash := filepath.ToSlash(path)
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
			strings.HasPrefix(slash, "../ms-queues/") || slash == "../platform/amqpx/amqpx.go" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, f := range forbidden {
			if strings.Contains(string(b), f) {
				t.Errorf("%s usa %s fora do topology-init", slash, f)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A build tag faultinject (kill injetável e fetch em loopback, só para teste) nunca pode
// chegar à imagem final nem ao compose: nada de produção a menciona.
func TestFaultinjectNaoVaiParaImagemNemCompose(t *testing.T) {
	var files []string
	err := filepath.WalkDir(filepath.Join(".."), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		switch name := d.Name(); {
		case name == "Dockerfile", name == "run.sh", strings.HasPrefix(name, "docker-compose"):
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("nenhum Dockerfile/compose encontrado: o teste não está olhando o repositório")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "faultinject") || strings.Contains(string(b), "WORKER_TEST_") {
			t.Errorf("%s menciona faultinject/WORKER_TEST_: não pode ir para a imagem", f)
		}
	}
}
