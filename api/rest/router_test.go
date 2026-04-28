package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
	"github.com/vectorcore/vectorcore-mmsc/internal/smpp"
)

func TestRouterListsMessagesAndRuntime(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	if err := repo.CreateMessage(context.Background(), &message.Message{
		ID:            "mid-1",
		TransactionID: "txn-1",
		Status:        message.StatusQueued,
		Direction:     message.DirectionMO,
		Origin:        message.InterfaceMM1,
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		ContentType:   "application/vnd.wap.mms-message",
	}); err != nil {
		t.Fatalf("create message: %v", err)
	}

	runtimeStore := config.NewRuntimeStore()
	runtimeStore.Replace(config.RuntimeSnapshot{
		Peers:         []db.MM4Peer{{Domain: "peer.example.net"}},
		MM3Relay:      &db.MM3Relay{Enabled: true, SMTPHost: "smtp.example.net", SMTPPort: 25},
		VASPs:         []db.MM7VASP{{VASPID: "vasp-1"}},
		SMPPUpstreams: []db.SMPPUpstream{{Name: "primary"}},
		Adaptation:    []db.AdaptationClass{{Name: "default"}},
	})
	smppManager := smpp.NewManager()
	if err := smppManager.Refresh(runtimeStore.Snapshot()); err != nil {
		t.Fatalf("refresh smpp manager: %v", err)
	}

	handler := NewRouter(config.Default(), repo, runtimeStore, smppManager, nil, "0.0.5d", time.Unix(0, 0))

	msgReq := httptest.NewRequest(http.MethodGet, "/api/v1/messages?status=queued", nil)
	msgRec := httptest.NewRecorder()
	handler.ServeHTTP(msgRec, msgReq)
	if msgRec.Code != http.StatusOK {
		t.Fatalf("unexpected messages status: %d", msgRec.Code)
	}
	if !bytes.Contains(msgRec.Body.Bytes(), []byte(`"mid-1"`)) {
		t.Fatalf("unexpected messages body: %s", msgRec.Body.String())
	}

	runtimeReq := httptest.NewRequest(http.MethodGet, "/api/v1/runtime", nil)
	runtimeRec := httptest.NewRecorder()
	handler.ServeHTTP(runtimeRec, runtimeReq)
	if runtimeRec.Code != http.StatusOK {
		t.Fatalf("unexpected runtime status: %d", runtimeRec.Code)
	}
	if !bytes.Contains(runtimeRec.Body.Bytes(), []byte(`"peer.example.net"`)) {
		t.Fatalf("unexpected runtime body: %s", runtimeRec.Body.String())
	}
	if !bytes.Contains(runtimeRec.Body.Bytes(), []byte(`"mm3_relay"`)) {
		t.Fatalf("unexpected runtime body: %s", runtimeRec.Body.String())
	}
	if !bytes.Contains(runtimeRec.Body.Bytes(), []byte(`"adaptation"`)) {
		t.Fatalf("unexpected runtime body: %s", runtimeRec.Body.String())
	}
}

func TestRouterGetsAndUpdatesMM3Relay(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	handler := NewRouter(config.Default(), repo, config.NewRuntimeStore(), smpp.NewManager(), nil, "0.0.5d", time.Unix(0, 0))

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/mm3/relay", nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("unexpected mm3 get status: %d body=%s", getRec.Code, getRec.Body.String())
	}

	body, _ := json.Marshal(db.MM3Relay{
		Enabled:             true,
		SMTPHost:            "smtp.example.net",
		SMTPPort:            2525,
		SMTPAuth:            true,
		SMTPUser:            "relay-user",
		SMTPPass:            "relay-pass",
		TLSEnabled:          true,
		DefaultSenderDomain: "mmsc.example.net",
		DefaultFromAddress:  "mmsc@mmsc.example.net",
	})
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/mm3/relay", bytes.NewReader(body))
	putRec := httptest.NewRecorder()
	handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("unexpected mm3 put status: %d body=%s", putRec.Code, putRec.Body.String())
	}

	relay, err := repo.GetMM3Relay(context.Background())
	if err != nil {
		t.Fatalf("get mm3 relay: %v", err)
	}
	if relay == nil || relay.SMTPHost != "smtp.example.net" || !relay.Enabled {
		t.Fatalf("unexpected mm3 relay: %#v", relay)
	}
}

func TestRouterReturnsSystemStatus(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	if err := repo.CreateMessage(context.Background(), &message.Message{
		ID:            "mid-oam",
		TransactionID: "txn-oam",
		Status:        message.StatusQueued,
		Direction:     message.DirectionMO,
		Origin:        message.InterfaceMM1,
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		ContentType:   "application/vnd.wap.mms-message",
	}); err != nil {
		t.Fatalf("create message: %v", err)
	}

	handler := NewRouter(config.Default(), repo, config.NewRuntimeStore(), smpp.NewManager(), nil, "0.0.5d", time.Unix(0, 0))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected system status code: %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"version":"0.0.5d"`)) {
		t.Fatalf("unexpected system status body: %s", rec.Body.String())
	}
}

func TestRouterListsMessageSMPPSubmissions(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	if err := repo.CreateMessage(context.Background(), &message.Message{
		ID:            "mid-smpp",
		TransactionID: "txn-smpp",
		Status:        message.StatusDelivering,
		Direction:     message.DirectionMT,
		Origin:        message.InterfaceMM1,
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		ContentType:   "application/vnd.wap.mms-message",
	}); err != nil {
		t.Fatalf("create message: %v", err)
	}
	if err := repo.CreateSMPPSubmission(context.Background(), db.SMPPSubmission{
		UpstreamName:      "primary",
		SMPPMessageID:     "smpp-1",
		InternalMessageID: "mid-smpp",
		Recipient:         "+12025550101",
		SegmentIndex:      0,
		SegmentCount:      2,
		State:             db.SMPPSubmissionPending,
	}); err != nil {
		t.Fatalf("create smpp submission: %v", err)
	}

	handler := NewRouter(config.Default(), repo, config.NewRuntimeStore(), smpp.NewManager(), nil, "0.0.5d", time.Unix(0, 0))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/messages/mid-smpp/smpp-submissions", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected smpp submissions status: %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"smpp-1"`)) {
		t.Fatalf("unexpected smpp submissions body: %s", rec.Body.String())
	}
}

func TestRouterListsMessageEvents(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	if err := repo.CreateMessage(context.Background(), &message.Message{
		ID:            "mid-events",
		TransactionID: "txn-events",
		Status:        message.StatusQueued,
		Direction:     message.DirectionMO,
		Origin:        message.InterfaceMM1,
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		ContentType:   "application/vnd.wap.mms-message",
	}); err != nil {
		t.Fatalf("create message: %v", err)
	}
	if err := repo.AppendMessageEvent(context.Background(), db.MessageEvent{
		MessageID: "mid-events",
		Source:    "operator",
		Type:      "note",
		Summary:   "Manual note",
		Detail:    "inspection requested",
	}); err != nil {
		t.Fatalf("append message event: %v", err)
	}

	handler := NewRouter(config.Default(), repo, config.NewRuntimeStore(), smpp.NewManager(), nil, "0.0.5d", time.Unix(0, 0))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/messages/mid-events/events", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected message events status: %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"Manual note"`)) {
		t.Fatalf("unexpected message events body: %s", rec.Body.String())
	}
}

func TestRouterDeletesDeliveringMessage(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	if err := repo.CreateMessage(context.Background(), &message.Message{
		ID:            "mid-delete-delivering",
		TransactionID: "txn-delete-delivering",
		Status:        message.StatusDelivering,
		Direction:     message.DirectionMO,
		Origin:        message.InterfaceMM1,
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		ContentType:   "application/vnd.wap.mms-message",
	}); err != nil {
		t.Fatalf("create message: %v", err)
	}

	handler := NewRouter(config.Default(), repo, config.NewRuntimeStore(), smpp.NewManager(), nil, "0.0.5d", time.Unix(0, 0))
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/messages/mid-delete-delivering", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent && rec.Code != http.StatusOK {
		t.Fatalf("unexpected delete status: %d body=%s", rec.Code, rec.Body.String())
	}

	if _, err := repo.GetMessage(context.Background(), "mid-delete-delivering"); err == nil {
		t.Fatal("expected deleted message lookup to fail")
	}
}

func TestRouterPostsMessageActions(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	if err := repo.CreateMessage(context.Background(), &message.Message{
		ID:            "mid-action",
		TransactionID: "txn-action",
		Status:        message.StatusDelivering,
		Direction:     message.DirectionMT,
		Origin:        message.InterfaceMM1,
		From:          "+12025550100",
		To:            []string{"+12025550101"},
		ContentType:   "application/vnd.wap.mms-message",
	}); err != nil {
		t.Fatalf("create message: %v", err)
	}

	handler := NewRouter(config.Default(), repo, config.NewRuntimeStore(), smpp.NewManager(), nil, "0.0.1d", time.Unix(0, 0))

	noteBody, _ := json.Marshal(map[string]string{
		"action": "note",
		"note":   "manual inspection started",
	})
	noteReq := httptest.NewRequest(http.MethodPost, "/api/v1/messages/mid-action/actions", bytes.NewReader(noteBody))
	noteRec := httptest.NewRecorder()
	handler.ServeHTTP(noteRec, noteReq)
	if noteRec.Code != http.StatusOK {
		t.Fatalf("unexpected message note action status: %d body=%s", noteRec.Code, noteRec.Body.String())
	}

	requeueBody, _ := json.Marshal(map[string]string{
		"action": "requeue",
		"note":   "retry requested after peer issue",
	})
	requeueReq := httptest.NewRequest(http.MethodPost, "/api/v1/messages/mid-action/actions", bytes.NewReader(requeueBody))
	requeueRec := httptest.NewRecorder()
	handler.ServeHTTP(requeueRec, requeueReq)
	if requeueRec.Code != http.StatusOK {
		t.Fatalf("unexpected message requeue action status: %d body=%s", requeueRec.Code, requeueRec.Body.String())
	}

	msg, err := repo.GetMessage(context.Background(), "mid-action")
	if err != nil {
		t.Fatalf("get message: %v", err)
	}
	if msg.Status != message.StatusQueued {
		t.Fatalf("expected queued status after requeue, got %v", msg.Status)
	}

	events, err := repo.ListMessageEvents(context.Background(), "mid-action", 20)
	if err != nil {
		t.Fatalf("list message events: %v", err)
	}
	var sawNote bool
	var sawRequeue bool
	for _, event := range events {
		if event.Source == "operator" && event.Type == "note" && event.Detail == "manual inspection started" {
			sawNote = true
		}
		if event.Source == "operator" && event.Type == "requeue" {
			sawRequeue = true
		}
	}
	if !sawNote || !sawRequeue {
		t.Fatalf("expected operator note and requeue events, got %#v", events)
	}
}

func TestRouterUpsertsPeer(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	handler := NewRouter(config.Default(), repo, config.NewRuntimeStore(), smpp.NewManager(), nil, "0.0.1d", time.Unix(0, 0))

	peerBody, _ := json.Marshal(db.MM4Peer{
		Domain:     "peer.example.net",
		SMTPHost:   "smtp.peer.example.net",
		SMTPPort:   25,
		TLSEnabled: true,
		Active:     true,
	})
	peerReq := httptest.NewRequest(http.MethodPost, "/api/v1/peers", bytes.NewReader(peerBody))
	peerRec := httptest.NewRecorder()
	handler.ServeHTTP(peerRec, peerReq)
	if peerRec.Code != http.StatusCreated {
		t.Fatalf("unexpected peer status: %d body=%s", peerRec.Code, peerRec.Body.String())
	}

	peers, err := repo.ListMM4Peers(context.Background())
	if err != nil || len(peers) != 1 {
		t.Fatalf("unexpected peers: %#v err=%v", peers, err)
	}

	peerBody, _ = json.Marshal(db.MM4Peer{
		SMTPHost:   "smtp2.peer.example.net",
		SMTPPort:   2525,
		TLSEnabled: false,
		Active:     false,
	})
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/peers/peer.example.net", bytes.NewReader(peerBody))
	putRec := httptest.NewRecorder()
	handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("unexpected peer put status: %d body=%s", putRec.Code, putRec.Body.String())
	}

	peers, err = repo.ListMM4Peers(context.Background())
	if err != nil || len(peers) != 1 || peers[0].SMTPHost != "smtp2.peer.example.net" || peers[0].Active {
		t.Fatalf("unexpected updated peers: %#v err=%v", peers, err)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/peers/peer.example.net", nil)
	delRec := httptest.NewRecorder()
	handler.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent && delRec.Code != http.StatusOK {
		t.Fatalf("unexpected peer delete status: %d body=%s", delRec.Code, delRec.Body.String())
	}

	peers, err = repo.ListMM4Peers(context.Background())
	if err != nil || len(peers) != 0 {
		t.Fatalf("unexpected peers after delete: %#v err=%v", peers, err)
	}
}

func TestRouterUpsertsMM4Route(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	handler := NewRouter(config.Default(), repo, config.NewRuntimeStore(), smpp.NewManager(), nil, "0.0.1d", time.Unix(0, 0))

	if err := repo.UpsertMM4Peer(context.Background(), db.MM4Peer{
		Name:       "Carrier A",
		Domain:     "carrier-a.example.net",
		SMTPHost:   "smtp.carrier-a.example.net",
		SMTPPort:   25,
		TLSEnabled: true,
		Active:     true,
	}); err != nil {
		t.Fatalf("upsert peer: %v", err)
	}

	routeBody, _ := json.Marshal(db.MM4Route{
		Name:             "Carrier A prefix",
		MatchType:        "msisdn_prefix",
		MatchValue:       "+1202555",
		EgressPeerDomain: "carrier-a.example.net",
		Priority:         100,
		Active:           true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mm4/routes", bytes.NewReader(routeBody))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("unexpected route status: %d body=%s", rec.Code, rec.Body.String())
	}

	routes, err := repo.ListMM4Routes(context.Background())
	if err != nil || len(routes) != 1 {
		t.Fatalf("unexpected routes: %#v err=%v", routes, err)
	}

	routes[0].Priority = 200
	routeBody, _ = json.Marshal(routes[0])
	putReq := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/mm4/routes/%d", routes[0].ID), bytes.NewReader(routeBody))
	putRec := httptest.NewRecorder()
	handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("unexpected route put status: %d body=%s", putRec.Code, putRec.Body.String())
	}

	delReq := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/mm4/routes/%d", routes[0].ID), nil)
	delRec := httptest.NewRecorder()
	handler.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent && delRec.Code != http.StatusOK {
		t.Fatalf("unexpected route delete status: %d body=%s", delRec.Code, delRec.Body.String())
	}
}

func TestRouterUpsertsAndDeletesVASP(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	handler := NewRouter(config.Default(), repo, config.NewRuntimeStore(), smpp.NewManager(), nil, "0.0.1d", time.Unix(0, 0))

	vaspBody, _ := json.Marshal(db.MM7VASP{
		VASPID:       "vasp-1",
		VASID:        "service-1",
		Protocol:     "soap",
		Version:      "5.3.0",
		SharedSecret: "secret",
		AllowedIPs:   []string{"198.51.100.10"},
		DeliverURL:   "https://vasp.example.net/deliver",
		ReportURL:    "https://vasp.example.net/report",
		MaxMsgSize:   1048576,
		Active:       true,
	})
	vaspReq := httptest.NewRequest(http.MethodPost, "/api/v1/vasps", bytes.NewReader(vaspBody))
	vaspRec := httptest.NewRecorder()
	handler.ServeHTTP(vaspRec, vaspReq)
	if vaspRec.Code != http.StatusCreated {
		t.Fatalf("unexpected vasp post status: %d body=%s", vaspRec.Code, vaspRec.Body.String())
	}

	vasps, err := repo.ListMM7VASPs(context.Background())
	if err != nil || len(vasps) != 1 {
		t.Fatalf("unexpected vasps after post: %#v err=%v", vasps, err)
	}

	vaspBody, _ = json.Marshal(db.MM7VASP{
		VASID:      "service-2",
		Protocol:   "eaif",
		Version:    "3.0",
		DeliverURL: "https://vasp.example.net/eaif",
		ReportURL:  "https://vasp.example.net/reports",
		MaxMsgSize: 2097152,
		Active:     false,
	})
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/vasps/vasp-1", bytes.NewReader(vaspBody))
	putRec := httptest.NewRecorder()
	handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("unexpected vasp put status: %d body=%s", putRec.Code, putRec.Body.String())
	}

	vasps, err = repo.ListMM7VASPs(context.Background())
	if err != nil || len(vasps) != 1 || vasps[0].Protocol != "eaif" || vasps[0].Active {
		t.Fatalf("unexpected vasps after put: %#v err=%v", vasps, err)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/vasps/vasp-1", nil)
	delRec := httptest.NewRecorder()
	handler.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent && delRec.Code != http.StatusOK {
		t.Fatalf("unexpected vasp delete status: %d body=%s", delRec.Code, delRec.Body.String())
	}

	vasps, err = repo.ListMM7VASPs(context.Background())
	if err != nil || len(vasps) != 0 {
		t.Fatalf("unexpected vasps after delete: %#v err=%v", vasps, err)
	}
}

func TestRouterUpsertsSMPPUpstreamAndPatchesMessageStatus(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	if err := repo.CreateMessage(context.Background(), &message.Message{
		ID:            "mid-2",
		TransactionID: "txn-2",
		Status:        message.StatusQueued,
		Direction:     message.DirectionMT,
		Origin:        message.InterfaceMM7,
		From:          "12345",
		To:            []string{"+12025550101"},
		ContentType:   "application/vnd.wap.mms-message",
	}); err != nil {
		t.Fatalf("create message: %v", err)
	}

	handler := NewRouter(config.Default(), repo, config.NewRuntimeStore(), smpp.NewManager(), nil, "0.0.1d", time.Unix(0, 0))

	upstreamBody, _ := json.Marshal(db.SMPPUpstream{
		Name:          "primary",
		Host:          "smsc.example.net",
		Port:          2775,
		SystemID:      "mmsc",
		Password:      "secret",
		BindMode:      "transceiver",
		EnquireLink:   30,
		ReconnectWait: 5,
		Active:        true,
	})
	upstreamReq := httptest.NewRequest(http.MethodPost, "/api/v1/smpp/upstreams", bytes.NewReader(upstreamBody))
	upstreamRec := httptest.NewRecorder()
	handler.ServeHTTP(upstreamRec, upstreamReq)
	if upstreamRec.Code != http.StatusCreated {
		t.Fatalf("unexpected upstream status: %d body=%s", upstreamRec.Code, upstreamRec.Body.String())
	}

	statusBody := []byte(`{"status":"delivered"}`)
	statusReq := httptest.NewRequest(http.MethodPatch, "/api/v1/messages/mid-2/status", bytes.NewReader(statusBody))
	statusRec := httptest.NewRecorder()
	handler.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("unexpected status patch code: %d body=%s", statusRec.Code, statusRec.Body.String())
	}

	upstreams, err := repo.ListSMPPUpstreams(context.Background())
	if err != nil || len(upstreams) != 1 {
		t.Fatalf("unexpected upstreams: %#v err=%v", upstreams, err)
	}
	updated, err := repo.GetMessage(context.Background(), "mid-2")
	if err != nil {
		t.Fatalf("get message after patch: %v", err)
	}
	if updated.Status != message.StatusDelivered {
		t.Fatalf("unexpected patched status: %#v", updated)
	}

	upstreamBody, _ = json.Marshal(db.SMPPUpstream{
		Host:          "smsc2.example.net",
		Port:          2776,
		SystemID:      "mmsc2",
		Password:      "secret2",
		BindMode:      "receiver",
		EnquireLink:   15,
		ReconnectWait: 6,
		Active:        false,
	})
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/smpp/upstreams/primary", bytes.NewReader(upstreamBody))
	putRec := httptest.NewRecorder()
	handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("unexpected upstream put status: %d body=%s", putRec.Code, putRec.Body.String())
	}

	upstreams, err = repo.ListSMPPUpstreams(context.Background())
	if err != nil || len(upstreams) != 1 || upstreams[0].Host != "smsc2.example.net" || upstreams[0].BindMode != "receiver" || upstreams[0].Active {
		t.Fatalf("unexpected updated upstreams: %#v err=%v", upstreams, err)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/smpp/upstreams/primary", nil)
	delRec := httptest.NewRecorder()
	handler.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent && delRec.Code != http.StatusOK {
		t.Fatalf("unexpected upstream delete status: %d body=%s", delRec.Code, delRec.Body.String())
	}

	upstreams, err = repo.ListSMPPUpstreams(context.Background())
	if err != nil || len(upstreams) != 0 {
		t.Fatalf("unexpected upstreams after delete: %#v err=%v", upstreams, err)
	}
}

func TestRouterUpsertsAndDeletesAdaptationClass(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	handler := NewRouter(config.Default(), repo, config.NewRuntimeStore(), smpp.NewManager(), nil, "0.0.1d", time.Unix(0, 0))

	body, _ := json.Marshal(db.AdaptationClass{
		Name:              "low-end",
		MaxMsgSizeBytes:   65536,
		MaxImageWidth:     320,
		MaxImageHeight:    240,
		AllowedImageTypes: []string{"image/jpeg"},
		AllowedAudioTypes: []string{"audio/amr"},
		AllowedVideoTypes: []string{"video/3gpp"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/adaptation/classes", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("unexpected adaptation status: %d body=%s", rec.Code, rec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/adaptation/classes", nil)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK || !bytes.Contains(listRec.Body.Bytes(), []byte(`"low-end"`)) {
		t.Fatalf("unexpected adaptation list response: %d %s", listRec.Code, listRec.Body.String())
	}

	body, _ = json.Marshal(db.AdaptationClass{
		MaxMsgSizeBytes:   131072,
		MaxImageWidth:     480,
		MaxImageHeight:    320,
		AllowedImageTypes: []string{"image/jpeg", "image/png"},
		AllowedAudioTypes: []string{"audio/amr"},
		AllowedVideoTypes: []string{"video/3gpp"},
	})
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/adaptation/classes/low-end", bytes.NewReader(body))
	putRec := httptest.NewRecorder()
	handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("unexpected adaptation put response: %d %s", putRec.Code, putRec.Body.String())
	}

	class, err := repo.GetAdaptationClass(context.Background(), "low-end")
	if err != nil {
		t.Fatalf("get adaptation class: %v", err)
	}
	if class.MaxImageWidth != 480 || class.MaxMsgSizeBytes != 131072 {
		t.Fatalf("unexpected updated adaptation class: %#v", class)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/adaptation/classes/low-end", nil)
	delRec := httptest.NewRecorder()
	handler.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent && delRec.Code != http.StatusOK {
		t.Fatalf("unexpected adaptation delete response: %d %s", delRec.Code, delRec.Body.String())
	}

	listReq = httptest.NewRequest(http.MethodGet, "/api/v1/adaptation/classes", nil)
	listRec = httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK || bytes.Contains(listRec.Body.Bytes(), []byte(`"low-end"`)) {
		t.Fatalf("unexpected adaptation list after delete: %d %s", listRec.Code, listRec.Body.String())
	}
}

func TestRouterServesEmbeddedUI(t *testing.T) {
	t.Parallel()

	handler := NewRouter(config.Default(), newTestRepo(t), config.NewRuntimeStore(), smpp.NewManager(), nil, "0.0.1d", time.Unix(0, 0))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected ui status: %d", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("VectorCore MMSC")) {
		t.Fatalf("unexpected ui body: %s", rec.Body.String())
	}
}

func newTestRepo(t *testing.T) db.Repository {
	t.Helper()

	path := t.TempDir() + "/api.db"
	repo, err := db.Open(context.Background(), db.OpenOptions{
		Driver: "sqlite",
		DSN:    path,
	})
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := db.RunMigrations(context.Background(), repo, os.DirFS("../..")); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return repo
}
