package db

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

func TestRepositoryMessageLifecycle(t *testing.T) {
	t.Parallel()

	repo := newSQLiteRepoForTest(t)

	expiry := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	msg := &message.Message{
		ID:             "msg-123",
		TransactionID:  "txn-123",
		Status:         message.StatusQueued,
		Direction:      message.DirectionMO,
		Origin:         message.InterfaceMM1,
		From:           "+12025550100",
		To:             []string{"+12025550101", "+12025550102"},
		CC:             []string{"+12025550103"},
		Subject:        "hello",
		ContentType:    "application/vnd.wap.mms-message",
		MMSVersion:     "1.3",
		MessageClass:   message.ClassPersonal,
		Priority:       message.PriorityNormal,
		DeliveryReport: true,
		ReadReport:     true,
		Expiry:         &expiry,
		MessageSize:    1234,
		ContentPath:    "mmsc/2026/03/31/msg-123/assembled.mms",
		StoreID:        "store-123",
		OriginHost:     "mmsc.example.net",
	}

	if err := repo.CreateMessage(context.Background(), msg); err != nil {
		t.Fatalf("create message: %v", err)
	}

	got, err := repo.GetMessage(context.Background(), msg.ID)
	if err != nil {
		t.Fatalf("get message: %v", err)
	}
	if got.Subject != msg.Subject {
		t.Fatalf("unexpected subject: %q", got.Subject)
	}
	if len(got.To) != 2 || got.To[1] != "+12025550102" {
		t.Fatalf("unexpected To values: %#v", got.To)
	}
	if got.Expiry == nil || !got.Expiry.Equal(expiry) {
		t.Fatalf("unexpected expiry: %#v", got.Expiry)
	}

	filtered, err := repo.ListMessages(context.Background(), MessageFilter{
		Status:    statusPtr(message.StatusQueued),
		Direction: directionPtr(message.DirectionMO),
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ID != msg.ID {
		t.Fatalf("unexpected filtered messages: %#v", filtered)
	}

	if err := repo.UpdateMessageContent(context.Background(), msg.ID, MessageContentUpdate{
		Subject:     "updated",
		ContentType: "application/vnd.wap.mms-message",
		MessageSize: 4321,
		ContentPath: "mmsc/2026/03/31/msg-123/replaced.mms",
		StoreID:     "store-456",
	}); err != nil {
		t.Fatalf("update message content: %v", err)
	}

	updated, err := repo.GetMessage(context.Background(), msg.ID)
	if err != nil {
		t.Fatalf("get updated message: %v", err)
	}
	if updated.Subject != "updated" || updated.MessageSize != 4321 || updated.StoreID != "store-456" {
		t.Fatalf("unexpected updated message: %#v", updated)
	}
}

func TestRepositorySubscriberLifecycle(t *testing.T) {
	t.Parallel()

	repo := newSQLiteRepoForTest(t)

	sub := Subscriber{
		MSISDN:          "+12025550199",
		Enabled:         true,
		AdaptationClass: "default",
		MaxMessageSize:  512000,
		HomeMMSC:        "mmsc.example.net",
	}
	if err := repo.UpsertSubscriber(context.Background(), sub); err != nil {
		t.Fatalf("upsert subscriber: %v", err)
	}

	sub.Enabled = false
	if err := repo.UpsertSubscriber(context.Background(), sub); err != nil {
		t.Fatalf("update subscriber: %v", err)
	}

	got, err := repo.GetSubscriber(context.Background(), sub.MSISDN)
	if err != nil {
		t.Fatalf("get subscriber: %v", err)
	}
	if got.Enabled {
		t.Fatal("expected subscriber to be updated")
	}

	list, err := repo.ListSubscribers(context.Background())
	if err != nil {
		t.Fatalf("list subscribers: %v", err)
	}
	if len(list) != 1 || list[0].MSISDN != sub.MSISDN {
		t.Fatalf("unexpected subscribers: %#v", list)
	}
}

func TestRepositoryRuntimeConfigLifecycle(t *testing.T) {
	t.Parallel()

	repo := newSQLiteRepoForTest(t)

	if err := repo.UpsertMM4Peer(context.Background(), MM4Peer{
		Name:       "Peer Example",
		Domain:     "peer.example.net",
		SMTPHost:   "smtp.peer.example.net",
		SMTPPort:   2525,
		SMTPAuth:   true,
		SMTPUser:   "user",
		SMTPPass:   "pass",
		TLSEnabled: true,
		AllowedIPs: []string{"192.0.2.1", "192.0.2.2"},
		Active:     true,
	}); err != nil {
		t.Fatalf("upsert peer: %v", err)
	}
	peers, err := repo.ListMM4Peers(context.Background())
	if err != nil {
		t.Fatalf("list peers: %v", err)
	}
	if len(peers) != 1 || len(peers[0].AllowedIPs) != 2 {
		t.Fatalf("unexpected peers: %#v", peers)
	}
	if err := repo.UpsertMM4Route(context.Background(), MM4Route{
		Name:             "Peer prefix",
		MatchType:        "msisdn_prefix",
		MatchValue:       "+1202555",
		EgressPeerDomain: "peer.example.net",
		Priority:         100,
		Active:           true,
	}); err != nil {
		t.Fatalf("upsert route: %v", err)
	}
	routes, err := repo.ListMM4Routes(context.Background())
	if err != nil {
		t.Fatalf("list routes: %v", err)
	}
	if len(routes) != 1 || routes[0].EgressPeerDomain != "peer.example.net" {
		t.Fatalf("unexpected routes: %#v", routes)
	}

	if err := repo.UpsertMM7VASP(context.Background(), MM7VASP{
		VASPID:       "vasp-1",
		VASID:        "service-1",
		Protocol:     "eaif",
		Version:      "3.0",
		SharedSecret: "secret",
		AllowedIPs:   []string{"198.51.100.10"},
		DeliverURL:   "https://vasp.example.net/deliver",
		ReportURL:    "https://vasp.example.net/report",
		MaxMsgSize:   2048,
		Active:       true,
	}); err != nil {
		t.Fatalf("upsert vasp: %v", err)
	}
	vasps, err := repo.ListMM7VASPs(context.Background())
	if err != nil {
		t.Fatalf("list vasps: %v", err)
	}
	if len(vasps) != 1 || vasps[0].VASPID != "vasp-1" || vasps[0].Protocol != "eaif" || vasps[0].Version != "3.0" {
		t.Fatalf("unexpected vasps: %#v", vasps)
	}
	if err := repo.DeleteMM7VASP(context.Background(), "vasp-1"); err != nil {
		t.Fatalf("delete vasp: %v", err)
	}
	vasps, err = repo.ListMM7VASPs(context.Background())
	if err != nil {
		t.Fatalf("list vasps after delete: %v", err)
	}
	if len(vasps) != 0 {
		t.Fatalf("expected vasps to be deleted: %#v", vasps)
	}

	if err := repo.UpsertMM3Relay(context.Background(), MM3Relay{
		Enabled:             true,
		SMTPHost:            "smtp.example.net",
		SMTPPort:            2525,
		SMTPAuth:            true,
		SMTPUser:            "user",
		SMTPPass:            "pass",
		TLSEnabled:          true,
		DefaultSenderDomain: "mmsc.example.net",
		DefaultFromAddress:  "mmsc@mmsc.example.net",
	}); err != nil {
		t.Fatalf("upsert mm3 relay: %v", err)
	}
	relay, err := repo.GetMM3Relay(context.Background())
	if err != nil {
		t.Fatalf("get mm3 relay: %v", err)
	}
	if relay == nil || relay.SMTPHost != "smtp.example.net" || relay.DefaultSenderDomain != "mmsc.example.net" {
		t.Fatalf("unexpected mm3 relay: %#v", relay)
	}

	if err := repo.UpsertSMPPUpstream(context.Background(), SMPPUpstream{
		Name:          "primary",
		Host:          "smsc.example.net",
		Port:          2775,
		SystemID:      "mmsc",
		Password:      "secret",
		SystemType:    "test",
		BindMode:      "transceiver",
		EnquireLink:   15,
		ReconnectWait: 3,
		Active:        true,
	}); err != nil {
		t.Fatalf("upsert smpp upstream: %v", err)
	}
	upstreams, err := repo.ListSMPPUpstreams(context.Background())
	if err != nil {
		t.Fatalf("list smpp upstreams: %v", err)
	}
	if len(upstreams) != 1 || upstreams[0].Name != "primary" {
		t.Fatalf("unexpected upstreams: %#v", upstreams)
	}
	if err := repo.DeleteSMPPUpstream(context.Background(), "primary"); err != nil {
		t.Fatalf("delete smpp upstream: %v", err)
	}
	upstreams, err = repo.ListSMPPUpstreams(context.Background())
	if err != nil {
		t.Fatalf("list smpp upstreams after delete: %v", err)
	}
	if len(upstreams) != 0 {
		t.Fatalf("expected upstreams to be deleted: %#v", upstreams)
	}

	if err := repo.UpsertAdaptationClass(context.Background(), AdaptationClass{
		Name:              "low-end",
		MaxMsgSizeBytes:   65536,
		MaxImageWidth:     320,
		MaxImageHeight:    240,
		AllowedImageTypes: []string{"image/jpeg"},
		AllowedAudioTypes: []string{"audio/amr"},
		AllowedVideoTypes: []string{"video/3gpp"},
	}); err != nil {
		t.Fatalf("upsert adaptation class: %v", err)
	}
	class, err := repo.GetAdaptationClass(context.Background(), "low-end")
	if err != nil {
		t.Fatalf("get adaptation class: %v", err)
	}
	if class.MaxImageWidth != 320 || len(class.AllowedImageTypes) != 1 || class.AllowedImageTypes[0] != "image/jpeg" {
		t.Fatalf("unexpected adaptation class: %#v", class)
	}
	classes, err := repo.ListAdaptationClasses(context.Background())
	if err != nil {
		t.Fatalf("list adaptation classes: %v", err)
	}
	if len(classes) == 0 {
		t.Fatal("expected default adaptation class to exist")
	}
	if err := repo.DeleteAdaptationClass(context.Background(), "low-end"); err != nil {
		t.Fatalf("delete adaptation class: %v", err)
	}
	if _, err := repo.GetAdaptationClass(context.Background(), "low-end"); err == nil {
		t.Fatal("expected deleted adaptation class lookup to fail")
	}

	if err := repo.DeleteMM4Peer(context.Background(), "peer.example.net"); err != nil {
		t.Fatalf("delete mm4 peer: %v", err)
	}
	peers, err = repo.ListMM4Peers(context.Background())
	if err != nil {
		t.Fatalf("list peers after delete: %v", err)
	}
	if len(peers) != 0 {
		t.Fatalf("expected peers to be deleted: %#v", peers)
	}
}

func TestRepositorySMPPSubmissionLifecycle(t *testing.T) {
	t.Parallel()

	repo := newSQLiteRepoForTest(t)

	msg := &message.Message{
		ID:            "msg-smpp-1",
		TransactionID: "txn-smpp-1",
		Status:        message.StatusDelivering,
		Direction:     message.DirectionMT,
		Origin:        message.InterfaceMM1,
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		ContentType:   "application/vnd.wap.mms-message",
	}
	if err := repo.CreateMessage(context.Background(), msg); err != nil {
		t.Fatalf("create message: %v", err)
	}

	if err := repo.CreateSMPPSubmission(context.Background(), SMPPSubmission{
		UpstreamName:      "primary",
		SMPPMessageID:     "smpp-1",
		InternalMessageID: msg.ID,
		Recipient:         "+12025550101",
		SegmentIndex:      0,
		SegmentCount:      2,
		State:             SMPPSubmissionPending,
	}); err != nil {
		t.Fatalf("create submission: %v", err)
	}

	submission, err := repo.UpdateSMPPSubmissionReceipt(context.Background(), "primary", "smpp-1", SMPPSubmissionFailed, "UNDELIV")
	if err != nil {
		t.Fatalf("update submission receipt: %v", err)
	}
	if submission.InternalMessageID != msg.ID || submission.State != SMPPSubmissionFailed || submission.ErrorText != "UNDELIV" {
		t.Fatalf("unexpected submission: %#v", submission)
	}
	if !submission.CompletedAt.Valid {
		t.Fatalf("expected completed_at to be set: %#v", submission)
	}

	submissions, err := repo.ListSMPPSubmissions(context.Background(), msg.ID)
	if err != nil {
		t.Fatalf("list submissions: %v", err)
	}
	if len(submissions) != 1 || submissions[0].SMPPMessageID != "smpp-1" {
		t.Fatalf("unexpected submissions: %#v", submissions)
	}
}

func TestRepositoryMessageEventLifecycleAndCap(t *testing.T) {
	t.Parallel()

	repo := newSQLiteRepoForTest(t)

	msg := &message.Message{
		ID:            "msg-event-1",
		TransactionID: "txn-event-1",
		Status:        message.StatusQueued,
		Direction:     message.DirectionMO,
		Origin:        message.InterfaceMM1,
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		ContentType:   "application/vnd.wap.mms-message",
	}
	if err := repo.CreateMessage(context.Background(), msg); err != nil {
		t.Fatalf("create message: %v", err)
	}

	for i := 0; i < 105; i++ {
		if err := repo.AppendMessageEvent(context.Background(), MessageEvent{
			MessageID: msg.ID,
			Source:    "test",
			Type:      "step",
			Summary:   fmt.Sprintf("event-%03d", i),
			Detail:    "detail",
		}); err != nil {
			t.Fatalf("append message event %d: %v", i, err)
		}
	}

	events, err := repo.ListMessageEvents(context.Background(), msg.ID, 200)
	if err != nil {
		t.Fatalf("list message events: %v", err)
	}
	if len(events) != 100 {
		t.Fatalf("expected 100 capped events, got %d", len(events))
	}
	if events[0].Summary != "event-104" {
		t.Fatalf("expected newest event first, got %#v", events[0])
	}
	if events[len(events)-1].Summary != "event-005" {
		t.Fatalf("expected oldest retained event to be event-005, got %#v", events[len(events)-1])
	}
}

func newSQLiteRepoForTest(t *testing.T) Repository {
	t.Helper()

	path := t.TempDir() + "/repo.db"
	repo, err := Open(context.Background(), testSQLiteConfig(path))
	if err != nil {
		t.Fatalf("open sqlite repo: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })

	if err := RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return repo
}

func statusPtr(v message.Status) *message.Status {
	return &v
}

func directionPtr(v message.Direction) *message.Direction {
	return &v
}
