package database

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hkjang/Kkiit/internal/password"
)

//go:embed migrations/*.sql
var migrations embed.FS

func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse POSTGRES_DSN: %w", err)
	}
	cfg.MaxConns = 20
	cfg.MinConns = 2
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 15 * time.Minute
	cfg.HealthCheckPeriod = time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect PostgreSQL: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	return pool, nil
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", int64(0x4b6b696974)); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", int64(0x4b6b696974)) //nolint:errcheck
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version bigint PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		versionPart := strings.SplitN(entry.Name(), "_", 2)[0]
		version, err := strconv.ParseInt(versionPart, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid migration name %s", entry.Name())
		}
		var applied bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		sqlBytes, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, string(sqlBytes)); err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, version)
		}
		if err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func EnsureBootstrapAdmin(ctx context.Context, pool *pgxpool.Pool, admin, adminPassword string) error {
	passwordHash, err := password.Hash(adminPassword)
	if err != nil {
		return err
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var userID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM users WHERE lower(username)=lower($1)`, admin).Scan(&userID)
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	if err == pgx.ErrNoRows {
		userID = uuid.New()
		email := any(nil)
		if strings.Contains(admin, "@") {
			email = strings.ToLower(admin)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO users(id,username,email,password_hash,display_name) VALUES($1,$2,$3,$4,$5)`,
			userID, admin, email, passwordHash, "Kkiit 관리자"); err != nil {
			return err
		}
	} else {
		// Bootstrap credentials are one-time seed material. A later restart must
		// never overwrite a password changed through account administration.
		if _, err := tx.Exec(ctx, `UPDATE users SET status='active',updated_at=now() WHERE id=$1`, userID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_roles(user_id,role_code,granted_by) VALUES($1,'super_admin',$1) ON CONFLICT DO NOTHING`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
