package main

import (
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func OpenDB(config *Config) (db *gorm.DB, err error) {
	db, err = gorm.Open(postgres.Open(config.DatabaseUri), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(config.DatabaseMaxConns)
	sqlDB.SetMaxIdleConns(config.DatabaseMaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Duration(config.DatabaseConnMaxLifetime) * time.Second)
	err = db.AutoMigrate(&Invoice{})
	if err != nil {
		return nil, err
	}
	return db, nil
}

type Invoice struct {
	gorm.Model
	AddIndex uint64
}