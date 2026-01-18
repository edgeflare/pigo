package role

import (
	"context"
	"testing"

	"github.com/edgeflare/pigo/internal/testutil/pgtest"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

var testRoleNamePrefix = "test_role"

func TestList(t *testing.T) {
	pgtest.WithConn(t, func(conn *pgx.Conn) {
		roles, err := List(context.Background(), conn)
		require.NoError(t, err, "Failed to list roles")
		require.NotEmpty(t, roles, "No roles were listed from the database")
	})
}

func TestCreate(t *testing.T) {
	pgtest.WithConn(t, func(conn *pgx.Conn) {
		roleName := testRoleNamePrefix + "_create"
		// Ensure cleanup runs after the test
		t.Cleanup(func() {
			pgtest.WithConn(t, func(conn *pgx.Conn) {
				cleanupRole(t, conn, roleName)
			})
		})

		newRole := Role{
			Name:        roleName,
			Superuser:   false,
			Inherit:     true,
			CreateRole:  false,
			CreateDB:    false,
			CanLogin:    true,
			Replication: false,
			ConnLimit:   -1,
			Password:    "secure_password",
			Config:      []string{"search_path=public"},
		}
		err := Create(context.Background(), conn, newRole)
		require.NoError(t, err, "Failed to create role")

		// Verify that the role was created
		fetchedRole, err := Get(context.Background(), conn, newRole.Name)
		require.NoError(t, err, "Failed to get the created role")
		require.Equal(t, newRole.Name, fetchedRole.Name, "Fetched role name does not match")
	})
}

func TestUpdate(t *testing.T) {
	pgtest.WithConn(t, func(conn *pgx.Conn) {
		roleName := testRoleNamePrefix + "_update"
		// Ensure cleanup runs after the test
		t.Cleanup(func() {
			pgtest.WithConn(t, func(conn *pgx.Conn) {
				cleanupRole(t, conn, roleName)
			})
		})

		// First create a role to update
		initialRole := Role{
			Name:        roleName,
			Superuser:   false,
			Inherit:     true,
			CreateRole:  false,
			CreateDB:    false,
			CanLogin:    true,
			Replication: false,
			ConnLimit:   -1,
		}
		err := Create(context.Background(), conn, initialRole)
		require.NoError(t, err, "Failed to create initial role")

		// Update the role
		updatedRole := initialRole
		updatedRole.Superuser = true
		updatedRole.Password = "new_secure_password"

		err = Update(context.Background(), conn, updatedRole)
		require.NoError(t, err, "Failed to update role")

		// Verify that the role was updated
		fetchedRole, err := Get(context.Background(), conn, updatedRole.Name)
		require.NoError(t, err, "Failed to get the updated role")
		require.True(t, fetchedRole.Superuser, "Role Superuser status should be true")
	})
}

func TestGet(t *testing.T) {
	pgtest.WithConn(t, func(conn *pgx.Conn) {
		roleName := testRoleNamePrefix + "_get"
		// Ensure cleanup runs after the test
		t.Cleanup(func() {
			pgtest.WithConn(t, func(conn *pgx.Conn) {
				cleanupRole(t, conn, roleName)
			})
		})

		// Create a role to get
		testRole := Role{
			Name:        roleName,
			Superuser:   false,
			Inherit:     true,
			CreateRole:  false,
			CreateDB:    false,
			CanLogin:    true,
			Replication: false,
			ConnLimit:   -1,
		}
		err := Create(context.Background(), conn, testRole)
		require.NoError(t, err, "Failed to create role for testing Get")

		// Retrieve the role
		fetchedRole, err := Get(context.Background(), conn, testRole.Name)
		require.NoError(t, err, "Failed to get role")
		require.Equal(t, testRole.Name, fetchedRole.Name, "Fetched role name does not match")
	})
}

func TestDelete(t *testing.T) {
	pgtest.WithConn(t, func(conn *pgx.Conn) {
		roleName := testRoleNamePrefix + "_delete"
		// Add cleanup as a safeguard in case the test fails before deletion
		t.Cleanup(func() {
			pgtest.WithConn(t, func(conn *pgx.Conn) {
				cleanupRole(t, conn, roleName)
			})
		})

		// Create a role to delete
		roleToDelete := Role{
			Name:        roleName,
			Superuser:   false,
			Inherit:     true,
			CreateRole:  false,
			CreateDB:    false,
			CanLogin:    true,
			Replication: false,
			ConnLimit:   -1,
		}
		err := Create(context.Background(), conn, roleToDelete)
		require.NoError(t, err, "Failed to create role for deletion")

		// Delete the role
		err = Delete(context.Background(), conn, roleToDelete.Name)
		require.NoError(t, err, "Failed to delete role")

		// Verify that the role is deleted
		_, err = Get(context.Background(), conn, roleToDelete.Name)
		require.Error(t, err, "Expected error when getting deleted role")
		require.Equal(t, ErrRoleNotFound, err, "Error should be ErrRoleNotFound")
	})
}

// cleanupRole attempts to delete a role and logs any errors
func cleanupRole(t *testing.T, conn *pgx.Conn, roleName string) {
	err := Delete(context.Background(), conn, roleName)
	if err != nil && err != ErrRoleNotFound {
		t.Logf("Failed to cleanup role %s: %v", roleName, err)
	}
}
