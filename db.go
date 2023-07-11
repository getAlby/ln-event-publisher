package main

import (
	"time"

	"github.com/lightningnetwork/lnd/lnrpc"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func OpenDB(config *Config) (db *gorm.DB, err error) {
	db, err = gorm.Open(postgres.Open(config.DatabaseUri), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	//don't print warnings for slow sql
	//because we use db transactions that span the rabbitmq publish operation
	db.Logger.LogMode(logger.Error)
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(config.DatabaseMaxConns)
	sqlDB.SetMaxIdleConns(config.DatabaseMaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Duration(config.DatabaseConnMaxLifetime) * time.Second)
	err = db.AutoMigrate(&Invoice{}, &Payment{})
	if err != nil {
		return nil, err
	}
	return db, nil
}

type Invoice struct {
	gorm.Model
	AddIndex uint64
}

type Payment struct {
	gorm.Model
	PaymentHash    string
	CreationTimeNs int64
	Status         lnrpc.Payment_PaymentStatus
}
