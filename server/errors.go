package server

import (
	"fmt"

	"github.com/go-mysql-org/go-mysql/mysql"
)

func errAccessDenied() error {
	return mysql.NewError(mysql.ER_ACCESS_DENIED_ERROR, "Access denied")
}

func errDBAccessDenied(name string) error {
	return mysql.NewDefaultError(mysql.ER_DBACCESS_DENIED_ERROR, "", "localhost", name)
}

func errNotBound() error {
	return fmt.Errorf("connection is not bound to a database")
}
