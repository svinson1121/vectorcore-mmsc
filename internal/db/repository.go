package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

type Repository interface {
	Driver() string
	DB() *sql.DB
	Ping(context.Context) error
	CreateMessage(context.Context, *message.Message) error
	GetMessage(context.Context, string) (*message.Message, error)
	GetMessageByTransactionID(context.Context, string) (*message.Message, error)
	ListMessages(context.Context, MessageFilter) ([]message.Message, error)
	ListExpiredMessages(ctx context.Context, now time.Time) ([]message.Message, error)
	ListMessagesAfter(ctx context.Context, after time.Time, limit int) ([]message.Message, error)
	DeleteMessage(context.Context, string) error
	UpdateMessageStatus(context.Context, string, message.Status) error
	UpdateMessageContent(context.Context, string, MessageContentUpdate) error
	AppendMessageEvent(context.Context, MessageEvent) error
	ListMessageEvents(context.Context, string, int) ([]MessageEvent, error)
	UpsertSubscriber(context.Context, Subscriber) error
	GetSubscriber(context.Context, string) (*Subscriber, error)
	ListSubscribers(context.Context) ([]Subscriber, error)
	UpsertMM4Peer(context.Context, MM4Peer) error
	ListMM4Peers(context.Context) ([]MM4Peer, error)
	DeleteMM4Peer(context.Context, string) error
	UpsertMM4Route(context.Context, MM4Route) error
	ListMM4Routes(context.Context) ([]MM4Route, error)
	DeleteMM4Route(context.Context, int64) error
	UpsertMM3Relay(context.Context, MM3Relay) error
	GetMM3Relay(context.Context) (*MM3Relay, error)
	UpsertMM7VASP(context.Context, MM7VASP) error
	ListMM7VASPs(context.Context) ([]MM7VASP, error)
	DeleteMM7VASP(context.Context, string) error
	UpsertSMPPUpstream(context.Context, SMPPUpstream) error
	ListSMPPUpstreams(context.Context) ([]SMPPUpstream, error)
	DeleteSMPPUpstream(context.Context, string) error
	CreateSMPPSubmission(context.Context, SMPPSubmission) error
	UpdateSMPPSubmissionReceipt(context.Context, string, string, SMPPSubmissionState, string) (*SMPPSubmission, error)
	ListSMPPSubmissions(context.Context, string) ([]SMPPSubmission, error)
	UpsertAdaptationClass(context.Context, AdaptationClass) error
	GetAdaptationClass(context.Context, string) (*AdaptationClass, error)
	ListAdaptationClasses(context.Context) ([]AdaptationClass, error)
	DeleteAdaptationClass(context.Context, string) error
	Close() error
}

type OpenOptions struct {
	Driver       string
	DSN          string
	MaxOpenConns int
	MaxIdleConns int
}

func Open(ctx context.Context, cfg OpenOptions) (Repository, error) {
	switch cfg.Driver {
	case "postgres":
		return openPostgres(ctx, cfg)
	case "sqlite":
		return openSQLite(ctx, cfg)
	default:
		return nil, ErrUnsupportedDriver{Driver: cfg.Driver}
	}
}

type ErrUnsupportedDriver struct {
	Driver string
}

func (e ErrUnsupportedDriver) Error() string {
	return "unsupported database driver: " + e.Driver
}

type MessageFilter struct {
	Status    *message.Status
	Direction *message.Direction
	Limit     int
}

type MessageContentUpdate struct {
	Subject     string
	ContentType string
	MessageSize int64
	ContentPath string
	StoreID     string
}

type MessageEvent struct {
	ID        int64
	MessageID string
	Source    string
	Type      string
	Summary   string
	Detail    string
	CreatedAt sql.NullTime
}

type Subscriber struct {
	MSISDN          string
	Enabled         bool
	AdaptationClass string
	MaxMessageSize  int64
	HomeMMSC        string
}

type MM4Peer struct {
	Name       string
	Domain     string
	SMTPHost   string
	SMTPPort   int
	SMTPAuth   bool
	SMTPUser   string
	SMTPPass   string
	TLSEnabled bool
	AllowedIPs []string
	Active     bool
}

type MM4Route struct {
	ID               int64
	Name             string
	MatchType        string
	MatchValue       string
	EgressType       string
	EgressTarget     string
	EgressPeerDomain string
	Priority         int
	Active           bool
}

type MM3Relay struct {
	Enabled             bool
	SMTPHost            string
	SMTPPort            int
	SMTPAuth            bool
	SMTPUser            string
	SMTPPass            string
	TLSEnabled          bool
	DefaultSenderDomain string
	DefaultFromAddress  string
}

type MM7VASP struct {
	VASPID       string
	VASID        string
	Protocol     string
	Version      string
	SharedSecret string
	AllowedIPs   []string
	DeliverURL   string
	ReportURL    string
	MaxMsgSize   int64
	Active       bool
}

type SMPPUpstream struct {
	Name               string
	Host               string
	Port               int
	SystemID           string
	Password           string
	SystemType         string
	BindMode           string
	EnquireLink        int
	ReconnectWait      int
	RegisteredDelivery int
	Active             bool
}

type SMPPSubmissionState int

const (
	SMPPSubmissionPending SMPPSubmissionState = iota
	SMPPSubmissionDelivered
	SMPPSubmissionFailed
)

type SMPPSubmission struct {
	UpstreamName      string
	SMPPMessageID     string
	InternalMessageID string
	Recipient         string
	SegmentIndex      int
	SegmentCount      int
	State             SMPPSubmissionState
	ErrorText         string
	SubmittedAt       sql.NullTime
	CompletedAt       sql.NullTime
}

type AdaptationClass struct {
	Name              string
	MaxMsgSizeBytes   int64
	MaxImageWidth     int
	MaxImageHeight    int
	AllowedImageTypes []string
	AllowedAudioTypes []string
	AllowedVideoTypes []string
}
