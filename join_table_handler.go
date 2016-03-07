package gorm

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

type JoinTableHandlerInterface interface {
	Setup(relationship *Relationship, tableName string, source reflect.Type, destination reflect.Type)
	Table(db *DB) string
	Add(handler JoinTableHandlerInterface, db *DB, source interface{}, destination interface{}) error
	Delete(handler JoinTableHandlerInterface, db *DB, sources ...interface{}) error
	JoinWith(handler JoinTableHandlerInterface, db *DB, source interface{}) *DB
	SourceForeignKeys() []JoinTableForeignKey
	DestinationForeignKeys() []JoinTableForeignKey
}

type JoinTableForeignKey struct {
	DBName            string
	AssociationDBName string
}

type JoinTableSource struct {
	ModelType   reflect.Type
	ForeignKeys []JoinTableForeignKey
}

type JoinTableHandler struct {
	TableName   string          `sql:"-"`
	Source      JoinTableSource `sql:"-"`
	Destination JoinTableSource `sql:"-"`
}

func (s *JoinTableHandler) SourceForeignKeys() []JoinTableForeignKey {
	return s.Source.ForeignKeys
}

func (s *JoinTableHandler) DestinationForeignKeys() []JoinTableForeignKey {
	return s.Destination.ForeignKeys
}

func (s *JoinTableHandler) Setup(relationship *Relationship, tableName string, source reflect.Type, destination reflect.Type) {
	s.TableName = tableName

	s.Source = JoinTableSource{ModelType: source}
	for idx, dbName := range relationship.ForeignFieldNames {
		s.Source.ForeignKeys = append(s.Source.ForeignKeys, JoinTableForeignKey{
			DBName:            relationship.ForeignDBNames[idx],
			AssociationDBName: dbName,
		})
	}

	s.Destination = JoinTableSource{ModelType: destination}
	for idx, dbName := range relationship.AssociationForeignFieldNames {
		s.Destination.ForeignKeys = append(s.Destination.ForeignKeys, JoinTableForeignKey{
			DBName:            relationship.AssociationForeignDBNames[idx],
			AssociationDBName: dbName,
		})
	}
}

func (s JoinTableHandler) Table(db *DB) string {
	return s.TableName
}

func (s JoinTableHandler) GetSearchMap(db *DB, sources ...interface{}) map[string]interface{} {
	values := map[string]interface{}{}

	for _, source := range sources {
		scope := db.NewScope(source)
		modelType := scope.GetModelStruct().ModelType

		if s.Source.ModelType == modelType {
			for _, foreignKey := range s.Source.ForeignKeys {
				if field, ok := scope.FieldByName(foreignKey.AssociationDBName); ok {
					values[foreignKey.DBName] = field.Field.Interface()
				}
			}
		} else if s.Destination.ModelType == modelType {
			for _, foreignKey := range s.Destination.ForeignKeys {
				if field, ok := scope.FieldByName(foreignKey.AssociationDBName); ok {
					values[foreignKey.DBName] = field.Field.Interface()
				}
			}
		}
	}
	return values
}

func (s JoinTableHandler) Add(handler JoinTableHandlerInterface, db *DB, source1 interface{}, source2 interface{}) error {
	scope := db.NewScope("")
	searchMap := s.GetSearchMap(db, source1, source2)

	var assignColumns, binVars, conditions []string
	var values []interface{}
	for key, value := range searchMap {
		assignColumns = append(assignColumns, scope.Quote(key))
		binVars = append(binVars, `?`)
		conditions = append(conditions, fmt.Sprintf("%v = ?", scope.Quote(key)))
		values = append(values, value)
	}

	for _, value := range values {
		values = append(values, value)
	}

	quotedTable := scope.Quote(handler.Table(db))
	sql := fmt.Sprintf(
		"INSERT INTO %v (%v) SELECT %v %v WHERE NOT EXISTS (SELECT * FROM %v WHERE %v)",
		quotedTable,
		strings.Join(assignColumns, ","),
		strings.Join(binVars, ","),
		scope.Dialect().SelectFromDummyTable(),
		quotedTable,
		strings.Join(conditions, " AND "),
	)

	return db.Exec(sql, values...).Error
}

func (s JoinTableHandler) Delete(handler JoinTableHandlerInterface, db *DB, sources ...interface{}) error {
	var (
		scope      = db.NewScope(nil)
		conditions []string
		values     []interface{}
	)

	for key, value := range s.GetSearchMap(db, sources...) {
		conditions = append(conditions, fmt.Sprintf("%v = ?", scope.Quote(key)))
		values = append(values, value)
	}

	return db.Table(handler.Table(db)).Where(strings.Join(conditions, " AND "), values...).Delete("").Error
}

func (s JoinTableHandler) JoinWith(handler JoinTableHandlerInterface, db *DB, source interface{}) *DB {
	var (
		scope           = db.NewScope(source)
		tableName       = handler.Table(db)
		quotedTableName = scope.Quote(tableName)
		joinConditions  []string
		values          []interface{}
	)

	if s.Source.ModelType == scope.GetModelStruct().ModelType {
		destinationTableName := db.NewScope(reflect.New(s.Destination.ModelType).Interface()).QuotedTableName()
		for _, foreignKey := range s.Destination.ForeignKeys {
			joinConditions = append(joinConditions, fmt.Sprintf("%v.%v = %v.%v", quotedTableName, scope.Quote(foreignKey.DBName), destinationTableName, scope.Quote(foreignKey.AssociationDBName)))
		}

		var foreignDBNames []string
		var foreignFieldNames []string

		for _, foreignKey := range s.Source.ForeignKeys {
			foreignDBNames = append(foreignDBNames, foreignKey.DBName)
			if field, ok := scope.FieldByName(foreignKey.AssociationDBName); ok {
				foreignFieldNames = append(foreignFieldNames, field.Name)
			}
		}

		foreignFieldValues := scope.getColumnAsArray(foreignFieldNames, scope.Value)

		var condString string
		if len(foreignFieldValues) > 0 {
			var quotedForeignDBNames []string
			for _, dbName := range foreignDBNames {
				quotedForeignDBNames = append(quotedForeignDBNames, tableName+"."+dbName)
			}

			condString = fmt.Sprintf("%v IN (%v)", toQueryCondition(scope, quotedForeignDBNames), toQueryMarks(foreignFieldValues))

			keys := scope.getColumnAsArray(foreignFieldNames, scope.Value)
			values = append(values, toQueryValues(keys))
		} else {
			condString = fmt.Sprintf("1 <> 1")
		}

		return db.Joins(fmt.Sprintf("INNER JOIN %v ON %v", quotedTableName, strings.Join(joinConditions, " AND "))).
			Where(condString, toQueryValues(foreignFieldValues)...)
	}

	db.Error = errors.New("wrong source type for join table handler")
	return db
}
