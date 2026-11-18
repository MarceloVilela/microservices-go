// Package db abre o pool do Postgres (pgx) e aplica as migrações embutidas.
package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ValidURL confere o formato de uma DATABASE_URL sem ecoar o valor (tem senha).
func ValidURL(s string) error {
	if _, err := pgxpool.ParseConfig(s); err != nil {
		return errors.New("não é uma URL postgres:// válida")
	}
	return nil
}

// Open abre o pool e confere a conexão. O erro não inclui a URL.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, errors.New("abrir pool do Postgres: configuração inválida")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		// a mensagem de erro de conexão do pgx não inclui a senha da URL
		return nil, fmt.Errorf("conectar ao Postgres: %w", err)
	}
	return pool, nil
}

type migration struct {
	version int
	sql     string
}

func load() ([]migration, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		num, _, ok := strings.Cut(e.Name(), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil {
			return nil, fmt.Errorf("migração %q: nome deve ser NNNN_descricao.sql", e.Name())
		}
		b, err := fs.ReadFile(migrationsFS, path.Join("migrations", e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, migration{v, string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// lockKey é a chave do pg_advisory_lock que serializa quem migra.
const lockKey = 7_201_118

// Migrate aplica as migrações pendentes, cada uma numa transação, sob um
// advisory lock (duas instâncias subindo juntas não correm uma contra a outra).
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	migs, err := load()
	if err != nil {
		return err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("adquirir conexão: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}
	// o unlock usa contexto próprio: precisa rodar mesmo com ctx cancelado
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", lockKey)
	}()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version int PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("schema_migrations: %w", err)
	}
	for _, m := range migs {
		var applied bool
		if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", m.version).Scan(&applied); err != nil {
			return fmt.Errorf("consultar migração %d: %w", m.version, err)
		}
		if applied {
			continue
		}
		if err := applyOne(ctx, conn, m); err != nil {
			return fmt.Errorf("migração %d: %w", m.version, err)
		}
	}
	return nil
}

func applyOne(ctx context.Context, conn *pgxpool.Conn, m migration) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, m.sql, pgx.QueryExecModeSimpleProtocol); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", m.version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
