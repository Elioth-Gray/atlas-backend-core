package database

import (
	"context"

	"gorm.io/gorm"
)

type txKey struct{}

// Transactor runs units of work inside a database transaction without leaking GORM into
// the service layer. Services depend on a small interface such as
//
//	type TxRunner interface {
//		WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
//	}
//
// and call repositories with the ctx passed to fn; repositories resolve the active
// transaction through Conn.
type Transactor struct {
	db *gorm.DB
}

// NewTransactor returns a Transactor backed by db.
func NewTransactor(db *gorm.DB) *Transactor {
	return &Transactor{db: db}
}

// WithinTx runs fn in a transaction. The transaction commits when fn returns nil and rolls
// back when it returns an error or panics. Nested calls reuse the outer transaction.
func (t *Transactor) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(*gorm.DB); ok {
		return fn(ctx)
	}
	return t.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
}

// Conn returns the transaction carried by ctx, or db when there is none, bound to ctx so
// cancellation and deadlines propagate to the query. Repositories must start every query
// with Conn(ctx, r.db).
func Conn(ctx context.Context, db *gorm.DB) *gorm.DB {
	if tx, ok := ctx.Value(txKey{}).(*gorm.DB); ok {
		return tx.WithContext(ctx)
	}
	return db.WithContext(ctx)
}
