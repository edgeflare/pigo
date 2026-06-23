package pglogrepl

import (
	"database/sql/driver"

	"github.com/jackc/pgx/v5/pgtype"
)

// uuidTextCodec is a pgtype.Codec for the UUID type that returns the
// text-format wire value as a plain Go string instead of [16]byte.
type uuidTextCodec struct{}

func (uuidTextCodec) FormatSupported(format int16) bool {
	return format == pgtype.TextFormatCode
}

func (uuidTextCodec) PreferredFormat() int16 {
	return pgtype.TextFormatCode
}

func (uuidTextCodec) PlanEncode(_ *pgtype.Map, _ uint32, _ int16, _ any) pgtype.EncodePlan {
	return nil
}

func (uuidTextCodec) PlanScan(_ *pgtype.Map, _ uint32, _ int16, _ any) pgtype.ScanPlan {
	return nil
}

func (uuidTextCodec) DecodeDatabaseSQLValue(_ *pgtype.Map, _ uint32, _ int16, src []byte) (driver.Value, error) {
	return string(src), nil
}

func (uuidTextCodec) DecodeValue(_ *pgtype.Map, _ uint32, _ int16, src []byte) (any, error) {
	return string(src), nil
}

// newTypeMap returns a pgtype.Map with pigo-specific codec overrides applied.
func newTypeMap() *pgtype.Map {
	m := pgtype.NewMap()
	m.RegisterType(&pgtype.Type{
		Name:  "uuid",
		OID:   pgtype.UUIDOID,
		Codec: uuidTextCodec{},
	})
	return m
}
