// Package role provides functions for managing PostgreSQL roles, including
// creating, updating, retrieving, and deleting roles.
package role

import (
	"context"
	"fmt"
	"strings"
	"time"

	pg "github.com/edgeflare/pigo/pkg/pgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrRoleNotFound is returned when a requested role does not exist in the database.
var ErrRoleNotFound = fmt.Errorf("role not found")

const roleSelectQuery = `SELECT rolname, rolsuper, rolinherit, rolcreaterole, rolcreatedb, rolcanlogin,
rolreplication, rolconnlimit, rolvaliduntil, rolbypassrls, rolconfig, oid FROM pg_roles`

// Role represents a PostgreSQL role with its associated attributes and privileges.
type Role struct {
	// Name is the name of the role.
	Name string `json:"rolname"`
	// Superuser indicates whether the role has superuser privileges.
	Superuser bool `json:"rolsuper"`
	// Inherit indicates whether the role inherits privileges from its parent roles.
	Inherit bool `json:"rolinherit"`
	// CreateRole indicates whether the role can create other roles.
	CreateRole bool `json:"rolcreaterole"`
	// CreateDB indicates whether the role can create databases.
	CreateDB bool `json:"rolcreatedb"`
	// CanLogin indicates whether the role can log in (applicable to user roles).
	CanLogin bool `json:"rolcanlogin"`
	// Replication indicates whether the role can replicate data.
	Replication bool `json:"rolreplication"`
	// ConnLimit is the maximum number of concurrent connections allowed for the role.
	ConnLimit int `json:"rolconnlimit"`
	// Password is the password hash (masked for security).
	Password string `json:"rolpassword"`
	// ValidUntil is the password expiry date (nullable).
	ValidUntil pgtype.Timestamptz `json:"rolvaliduntil"`
	// BypassRLS indicates whether the role can bypass row-level security policies.
	BypassRLS bool `json:"rolbypassrls"`
	// Config is an array of configuration settings for the role.
	Config []string `json:"rolconfig"`
	// OID is the object identifier (OID) of the role.
	OID uint32 `json:"oid"`
}

// List retrieves all PostgreSQL roles from the database.
//
// It queries the database using `SELECT * FROM pg_roles;` and returns a slice of Role, or any error encountered.
func List(ctx context.Context, conn pg.Conn) ([]Role, error) {
	rows, err := conn.Query(ctx, roleSelectQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to query roles: %w", err)
	}
	defer rows.Close()

	var roles []Role
	for rows.Next() {
		role, err := scanRole(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan role: %w", err)
		}
		roles = append(roles, *role)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over roles: %w", err)
	}

	return roles, nil
}

// Create adds a new PostgreSQL role to the database based on the provided Role struct.
// It returns an error if role already exists or the creation process fails
func Create(ctx context.Context, conn pg.Conn, role Role) error {
	return createOrAlterRole(ctx, conn, role, true)
}

// Update modifies an existing PostgreSQL role in the database.
// It updates the role's attributes based on the provided Role struct.
// If the role does not exist, an error is returned.
func Update(ctx context.Context, conn pg.Conn, role Role) error {
	return createOrAlterRole(ctx, conn, role, false)
}

// Get returns the role identified by roleName, or ErrRoleNotFound if it doesn't exists
func Get(ctx context.Context, conn pg.Conn, roleName string) (*Role, error) {
	var role *Role
	role, err := scanRole(conn.QueryRow(ctx, roleSelectQuery+` WHERE rolname = $1`, roleName))
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrRoleNotFound
		}
		return nil, fmt.Errorf("failed to get role: %w", err)
	}

	return role, nil
}

// Delete removes a PostgreSQL role from the database.
func Delete(ctx context.Context, conn pg.Conn, roleName string) error {
	deleteQuery := fmt.Sprintf("DROP ROLE IF EXISTS %s", pgx.Identifier{roleName}.Sanitize())

	_, err := conn.Exec(ctx, deleteQuery)
	if err != nil {
		return fmt.Errorf("failed to delete role: %w", err)
	}

	return nil
}

// scanRole scans a row into a Role struct.
func scanRole(row pgx.Row) (*Role, error) {
	var role Role
	err := row.Scan(
		&role.Name,
		&role.Superuser,
		&role.Inherit,
		&role.CreateRole,
		&role.CreateDB,
		&role.CanLogin,
		&role.Replication,
		&role.ConnLimit,
		&role.ValidUntil,
		&role.BypassRLS,
		&role.Config,
		&role.OID,
	)
	if err != nil {
		return nil, err
	}
	role.Password = "********"
	return &role, nil
}

// addRoleAttributes appends role attributes to the query builder
func addRoleAttributes(builder *strings.Builder, role Role) {
	attributes := []struct {
		condition bool
		attribute string
	}{
		{role.Superuser, "SUPERUSER"},
		{role.Replication, "REPLICATION"},
		{role.Inherit, "INHERIT"},
		{role.CreateRole, "CREATEROLE"},
		{role.CreateDB, "CREATEDB"},
		{role.CanLogin, "LOGIN"},
	}

	for _, attr := range attributes {
		if attr.condition {
			builder.WriteString(" " + attr.attribute)
		}
	}

	if role.ConnLimit == -1 || role.ConnLimit == 0 {
		builder.WriteString(" CONNECTION LIMIT -1")
	} else if role.ConnLimit > 0 {
		builder.WriteString(fmt.Sprintf(" CONNECTION LIMIT %d", role.ConnLimit))
	}

	if role.ValidUntil.Valid {
		builder.WriteString(fmt.Sprintf(" VALID UNTIL '%s'", role.ValidUntil.Time.Format(time.RFC3339)))
	}
}

func alterRoleConfigAndPassword(ctx context.Context, conn pg.Conn, role Role) error {
	if len(role.Config) > 0 {
		configStr := strings.Join(role.Config, ", ")
		alterQuery := fmt.Sprintf("ALTER ROLE %s SET %s", pgx.Identifier{role.Name}.Sanitize(), configStr)
		if _, err := conn.Exec(ctx, alterQuery); err != nil {
			return fmt.Errorf("failed to set role config: %w", err)
		}
	}

	if role.Password != "" {
		passwordQuery := fmt.Sprintf("ALTER ROLE %s WITH PASSWORD '%s'", pgx.Identifier{role.Name}.Sanitize(), role.Password)
		if _, err := conn.Exec(ctx, passwordQuery); err != nil {
			return fmt.Errorf("failed to update role password: %w", err)
		}
	}

	return nil
}

// createOrAlterRole constructs and executes a CREATE or ALTER ROLE query.
func createOrAlterRole(ctx context.Context, conn pg.Conn, role Role, isCreate bool) error {
	var queryBuilder strings.Builder
	if isCreate {
		queryBuilder.WriteString(fmt.Sprintf("CREATE ROLE %s", pgx.Identifier{role.Name}.Sanitize()))
	} else {
		queryBuilder.WriteString(fmt.Sprintf("ALTER ROLE %s", pgx.Identifier{role.Name}.Sanitize()))
	}

	addRoleAttributes(&queryBuilder, role)

	// Execute the CREATE/ALTER ROLE query
	if _, err := conn.Exec(ctx, queryBuilder.String()); err != nil {
		action := "create"
		if !isCreate {
			action = "update"
		}
		return fmt.Errorf("failed to %s role: %w", action, err)
	}

	return alterRoleConfigAndPassword(ctx, conn, role)
}
