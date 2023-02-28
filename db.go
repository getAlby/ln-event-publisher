package main

import (
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func OpenDB(config *Config) (db *gorm.DB, err error) {
	db, err = gorm.Open(postgres.Open(config.DatabaseUri), &gorm.Config{})
	if err != nil {
		return nil, err
	}
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
