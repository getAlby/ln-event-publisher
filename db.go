package main

import (
	"log"
	"os"
	"time"

	"github.com/lightningnetwork/lnd/lnrpc"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func OpenDB(config *Config) (db *gorm.DB, err error) {
	//overwrite logger so we don't print warnings for slow sql
	//because we use db transactions that span the rabbitmq publish operation
	dbLogger := logger.New(log.New(os.Stdout, "\r\n", log.LstdFlags), logger.Config{
		SlowThreshold:             200 * time.Millisecond,
		LogLevel:                  logger.Error,
		IgnoreRecordNotFoundError: false,
		Colorful:                  true,
	})
	db, err = gorm.Open(postgres.Open(config.DatabaseUri), &gorm.Config{
		Logger: dbLogger,
	})
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
	err = db.AutoMigrate(&Invoice{}, &Payment{})
	if err != nil {
		return nil, err
	}
	return db, nil
}

type Invoice struct {
	gorm.Model
	AddIndex    uint64
	SettleIndex uint64
}

type Payment struct {
	gorm.Model
	PaymentHash    string
	CreationTimeNs int64
	Status         lnrpc.Payment_PaymentStatus
}
