package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"

	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/lnrpc/routerrpc"
	"github.com/lightningnetwork/lnd/macaroons"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"gopkg.in/macaroon.v2"
)

func NewLNDclient(lndOptions LNDoptions) (result *LNDWrapper, err error) {
	var creds credentials.TransportCredentials
	if lndOptions.CertHex != "" {
		cp := x509.NewCertPool()
		cert, err := hex.DecodeString(lndOptions.CertHex)
		if err != nil {
			return nil, err
		}
		cp.AppendCertsFromPEM(cert)
		creds = credentials.NewClientTLSFromCert(cp, "")
	} else {
		creds = credentials.NewTLS(&tls.Config{})
	}
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
	}

	var macaroonData []byte
	if lndOptions.MacaroonHex != "" {
		macBytes, err := hex.DecodeString(lndOptions.MacaroonHex)
		if err != nil {
			return nil, err
		}
		macaroonData = macBytes
	} else {
		return nil, errors.New("LND macaroon is missing")
	}

	mac := &macaroon.Macaroon{}
	if err := mac.UnmarshalBinary(macaroonData); err != nil {
		return nil, err
	}
	macCred, err := macaroons.NewMacaroonCredential(mac)
	if err != nil {
		return nil, err
	}
	opts = append(opts, grpc.WithPerRPCCredentials(macCred))

	conn, err := grpc.Dial(lndOptions.Address, opts...)
	if err != nil {
		return nil, err
	}

	return &LNDWrapper{
		client:       lnrpc.NewLightningClient(conn),
		routerClient: routerrpc.NewRouterClient(conn),
	}, nil
}

type LNDoptions struct {
	Address     string
	CertHex     string
	MacaroonHex string
}

type LNDWrapper struct {
	client       lnrpc.LightningClient
	routerClient routerrpc.RouterClient
}
