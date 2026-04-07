package rest

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/message"
	"github.com/vectorcore/vectorcore-mmsc/internal/metrics"
	"github.com/vectorcore/vectorcore-mmsc/internal/smpp"
	"github.com/vectorcore/vectorcore-mmsc/internal/store"
)

type Router struct {
	cfg         *config.Config
	repo        db.Repository
	runtime     *config.RuntimeStore
	smppManager *smpp.Manager
	store       store.Store
	version     string
	startedAt   time.Time
}

func NewRouter(cfg *config.Config, repo db.Repository, runtimeStore *config.RuntimeStore, smppManager *smpp.Manager, contentStore store.Store, version string, startedAt time.Time) http.Handler {
	r := &Router{
		cfg:         cfg,
		repo:        repo,
		runtime:     runtimeStore,
		smppManager: smppManager,
		store:       contentStore,
		version:     version,
		startedAt:   startedAt,
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(metrics.Registry(), promhttp.HandlerOpts{}))

	humaCfg := huma.DefaultConfig("VectorCore MMSC Admin API", "0.1.0")
	api := humago.New(mux, humaCfg)

	huma.Get(api, "/healthz", r.getHealthz)
	huma.Get(api, "/readyz", r.getReadyz)
	huma.Get(api, "/api/v1/messages", r.listMessages)
	huma.Get(api, "/api/v1/messages/{id}", r.getMessage)
	huma.Get(api, "/api/v1/messages/{id}/events", r.listMessageEvents)
	huma.Get(api, "/api/v1/messages/{id}/smpp-submissions", r.listMessageSMPPSubmissions)
	huma.Delete(api, "/api/v1/messages/{id}", r.deleteMessage)
	huma.Patch(api, "/api/v1/messages/{id}/status", r.patchMessageStatus)
	huma.Post(api, "/api/v1/messages/{id}/actions", r.postMessageAction)
	huma.Get(api, "/api/v1/peers", r.listPeers)
	huma.Register(api, huma.Operation{
		OperationID:   "post-peer",
		Method:        http.MethodPost,
		Path:          "/api/v1/peers",
		DefaultStatus: http.StatusCreated,
	}, r.postPeer)
	huma.Put(api, "/api/v1/peers/{domain}", r.putPeer)
	huma.Delete(api, "/api/v1/peers/{domain}", r.deletePeer)
	huma.Get(api, "/api/v1/mm3/relay", r.getMM3Relay)
	huma.Put(api, "/api/v1/mm3/relay", r.putMM3Relay)
	huma.Get(api, "/api/v1/vasps", r.listVASPs)
	huma.Register(api, huma.Operation{
		OperationID:   "post-vasp",
		Method:        http.MethodPost,
		Path:          "/api/v1/vasps",
		DefaultStatus: http.StatusCreated,
	}, r.postVASP)
	huma.Put(api, "/api/v1/vasps/{vaspid}", r.putVASP)
	huma.Delete(api, "/api/v1/vasps/{vaspid}", r.deleteVASP)
	huma.Get(api, "/api/v1/smpp/upstreams", r.listSMPPUpstreams)
	huma.Register(api, huma.Operation{
		OperationID:   "post-smpp-upstream",
		Method:        http.MethodPost,
		Path:          "/api/v1/smpp/upstreams",
		DefaultStatus: http.StatusCreated,
	}, r.postSMPPUpstream)
	huma.Put(api, "/api/v1/smpp/upstreams/{name}", r.putSMPPUpstream)
	huma.Delete(api, "/api/v1/smpp/upstreams/{name}", r.deleteSMPPUpstream)
	huma.Get(api, "/api/v1/adaptation/classes", r.listAdaptationClasses)
	huma.Register(api, huma.Operation{
		OperationID:   "post-adaptation-class",
		Method:        http.MethodPost,
		Path:          "/api/v1/adaptation/classes",
		DefaultStatus: http.StatusCreated,
	}, r.postAdaptationClass)
	huma.Put(api, "/api/v1/adaptation/classes/{name}", r.putAdaptationClass)
	huma.Delete(api, "/api/v1/adaptation/classes/{name}", r.deleteAdaptationClass)
	huma.Get(api, "/api/v1/runtime", r.getRuntime)
	huma.Get(api, "/api/v1/smpp/status", r.getSMPPStatus)
	huma.Get(api, "/api/v1/system/status", r.getSystemStatus)
	huma.Get(api, "/api/v1/system/config", r.getSystemConfig)
	mountUI(mux)

	return mux
}

type statusBody struct {
	Status string `json:"status"`
}

type healthzOutput struct {
	Body map[string]any
}

type readyzOutput struct {
	Body map[string]any
}

type messagesInput struct {
	Limit     int    `query:"limit" default:"100"`
	Status    string `query:"status"`
	Direction string `query:"direction"`
}

type messagesOutput struct {
	Body struct {
		Messages []apiMessage `json:"messages"`
	}
}

type messageInput struct {
	ID string `path:"id"`
}

type messageOutput struct {
	Body apiMessage
}

type messageSMPPSubmissionsOutput struct {
	Body struct {
		Submissions []apiSMPPSubmission `json:"submissions"`
	}
}

type messageEventsOutput struct {
	Body struct {
		Events []apiMessageEvent `json:"events"`
	}
}

type messageStatusInput struct {
	ID   string `path:"id"`
	Body statusBody
}

type messageActionBody struct {
	Action string `json:"action"`
	Note   string `json:"note,omitempty"`
}

type messageActionInput struct {
	ID   string `path:"id"`
	Body messageActionBody
}

type peersOutput struct {
	Body struct {
		Peers []db.MM4Peer `json:"peers"`
	}
}

type peerInput struct {
	Body db.MM4Peer
}

type peerPathInput struct {
	Domain string `path:"domain"`
	Body   db.MM4Peer
}

type peerDeleteInput struct {
	Domain string `path:"domain"`
}

type peerOutput struct {
	Body db.MM4Peer
}

type mm3RelayInput struct {
	Body db.MM3Relay
}

type mm3RelayOutput struct {
	Body db.MM3Relay
}

type vaspsOutput struct {
	Body struct {
		VASPs []db.MM7VASP `json:"vasps"`
	}
}

type vaspInput struct {
	Body db.MM7VASP
}

type vaspPathInput struct {
	VASPID string `path:"vaspid"`
	Body   db.MM7VASP
}

type vaspDeleteInput struct {
	VASPID string `path:"vaspid"`
}

type vaspOutput struct {
	Body db.MM7VASP
}

type smppUpstreamsOutput struct {
	Body struct {
		Upstreams []db.SMPPUpstream `json:"upstreams"`
	}
}

type smppUpstreamInput struct {
	Body db.SMPPUpstream
}

type smppUpstreamPathInput struct {
	Name string `path:"name"`
	Body db.SMPPUpstream
}

type smppUpstreamDeleteInput struct {
	Name string `path:"name"`
}

type smppUpstreamOutput struct {
	Body db.SMPPUpstream
}

type runtimeOutput struct {
	Body map[string]any
}

type smppStatusOutput struct {
	Body map[string]any
}

type systemStatusOutput struct {
	Body map[string]any
}

type systemConfigOutput struct {
	Body map[string]any
}

type adaptationClassesOutput struct {
	Body struct {
		Classes []db.AdaptationClass `json:"classes"`
	}
}

type adaptationClassInput struct {
	Body db.AdaptationClass
}

type adaptationClassPathInput struct {
	Name string `path:"name"`
	Body db.AdaptationClass
}

type adaptationClassDeleteInput struct {
	Name string `path:"name"`
}

type adaptationClassOutput struct {
	Body db.AdaptationClass
}

type apiMessage struct {
	ID             string   `json:"ID"`
	TransactionID  string   `json:"TransactionID"`
	Status         int16    `json:"Status"`
	Direction      int16    `json:"Direction"`
	From           string   `json:"From"`
	To             []string `json:"To"`
	CC             []string `json:"CC,omitempty"`
	BCC            []string `json:"BCC,omitempty"`
	Subject        string   `json:"Subject,omitempty"`
	ContentType    string   `json:"ContentType,omitempty"`
	MMSVersion     string   `json:"MMSVersion,omitempty"`
	DeliveryReport bool     `json:"DeliveryReport"`
	ReadReport     bool     `json:"ReadReport"`
	MessageSize    int64    `json:"MessageSize"`
	ContentPath    string   `json:"ContentPath,omitempty"`
	StoreID        string   `json:"StoreID,omitempty"`
	Origin         int16    `json:"Origin"`
	OriginHost     string   `json:"OriginHost,omitempty"`
	ReceivedAt     string   `json:"ReceivedAt,omitempty"`
	UpdatedAt      string   `json:"UpdatedAt,omitempty"`
	Expiry         *string  `json:"Expiry,omitempty"`
	DeliveryTime   *string  `json:"DeliveryTime,omitempty"`
}

type apiMessageEvent struct {
	ID        int64  `json:"ID"`
	MessageID string `json:"MessageID"`
	Source    string `json:"Source"`
	Type      string `json:"Type"`
	Summary   string `json:"Summary"`
	Detail    string `json:"Detail"`
	CreatedAt string `json:"CreatedAt,omitempty"`
}

type apiSMPPSubmission struct {
	UpstreamName      string `json:"UpstreamName"`
	SMPPMessageID     string `json:"SMPPMessageID"`
	InternalMessageID string `json:"InternalMessageID"`
	Recipient         string `json:"Recipient"`
	SegmentIndex      int    `json:"SegmentIndex"`
	SegmentCount      int    `json:"SegmentCount"`
	State             int    `json:"State"`
	ErrorText         string `json:"ErrorText"`
	SubmittedAt       string `json:"SubmittedAt,omitempty"`
	CompletedAt       string `json:"CompletedAt,omitempty"`
}

func (r *Router) getHealthz(context.Context, *struct{}) (*healthzOutput, error) {
	return &healthzOutput{
		Body: map[string]any{"status": "ok"},
	}, nil
}

func (r *Router) getReadyz(ctx context.Context, _ *struct{}) (*readyzOutput, error) {
	if err := r.repo.Ping(ctx); err != nil {
		return nil, huma.Error503ServiceUnavailable("database not ready", err)
	}
	snapshot := r.runtime.Snapshot()
	return &readyzOutput{
		Body: map[string]any{
			"status":         "ready",
			"mm4_peers":      len(snapshot.Peers),
			"mm3_enabled":    snapshot.MM3Relay != nil && snapshot.MM3Relay.Enabled,
			"mm7_vasps":      len(snapshot.VASPs),
			"smpp_upstreams": len(snapshot.SMPPUpstreams),
		},
	}, nil
}

func (r *Router) listMessages(ctx context.Context, input *messagesInput) (*messagesOutput, error) {
	filter, err := parseMessageFilterValues(input.Limit, input.Status, input.Direction)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	items, err := r.repo.ListMessages(ctx, filter)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list messages", err)
	}
	resp := &messagesOutput{}
	resp.Body.Messages = make([]apiMessage, 0, len(items))
	for _, item := range items {
		resp.Body.Messages = append(resp.Body.Messages, toAPIMessage(item))
	}
	return resp, nil
}

func (r *Router) getMessage(ctx context.Context, input *messageInput) (*messageOutput, error) {
	msg, err := r.repo.GetMessage(ctx, input.ID)
	if err != nil {
		return nil, huma.Error404NotFound("message not found", err)
	}
	return &messageOutput{Body: toAPIMessage(*msg)}, nil
}

func (r *Router) listMessageSMPPSubmissions(ctx context.Context, input *messageInput) (*messageSMPPSubmissionsOutput, error) {
	if _, err := r.repo.GetMessage(ctx, input.ID); err != nil {
		return nil, huma.Error404NotFound("message not found", err)
	}
	items, err := r.repo.ListSMPPSubmissions(ctx, input.ID)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list smpp submissions", err)
	}
	resp := &messageSMPPSubmissionsOutput{}
	resp.Body.Submissions = make([]apiSMPPSubmission, 0, len(items))
	for _, item := range items {
		resp.Body.Submissions = append(resp.Body.Submissions, toAPISMPPSubmission(item))
	}
	return resp, nil
}

func (r *Router) listMessageEvents(ctx context.Context, input *messageInput) (*messageEventsOutput, error) {
	if _, err := r.repo.GetMessage(ctx, input.ID); err != nil {
		return nil, huma.Error404NotFound("message not found", err)
	}
	items, err := r.repo.ListMessageEvents(ctx, input.ID, 100)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list message events", err)
	}
	resp := &messageEventsOutput{}
	resp.Body.Events = make([]apiMessageEvent, 0, len(items))
	for _, item := range items {
		resp.Body.Events = append(resp.Body.Events, toAPIMessageEvent(item))
	}
	return resp, nil
}

func (r *Router) deleteMessage(ctx context.Context, input *messageInput) (*struct{}, error) {
	msg, err := r.repo.GetMessage(ctx, input.ID)
	if err != nil {
		return nil, huma.Error404NotFound("message not found", err)
	}
	if msg.Status != message.StatusQueued && msg.Status != message.StatusDelivering {
		return nil, huma.Error409Conflict("only queued or delivering messages can be deleted")
	}
	if err := r.repo.DeleteMessage(ctx, input.ID); err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, http.ErrMissingFile) {
			return &struct{}{}, nil
		}
		return nil, huma.Error500InternalServerError("failed to delete message", err)
	}
	r.deleteStoredMessageContent(ctx, *msg)
	return &struct{}{}, nil
}

func (r *Router) patchMessageStatus(ctx context.Context, input *messageStatusInput) (*messageOutput, error) {
	msg, err := r.repo.GetMessage(ctx, input.ID)
	if err != nil {
		return nil, huma.Error404NotFound("message not found", err)
	}
	status, ok := parseStatus(input.Body.Status)
	if !ok {
		return nil, huma.Error400BadRequest("invalid status value")
	}
	if err := r.repo.UpdateMessageStatus(ctx, input.ID, status); err != nil {
		return nil, huma.Error500InternalServerError("failed to update message status", err)
	}
	msg.Status = status
	msg.UpdatedAt = time.Now().UTC()
	return &messageOutput{Body: toAPIMessage(*msg)}, nil
}

func (r *Router) postMessageAction(ctx context.Context, input *messageActionInput) (*messageOutput, error) {
	msg, err := r.repo.GetMessage(ctx, input.ID)
	if err != nil {
		return nil, huma.Error404NotFound("message not found", err)
	}

	switch input.Body.Action {
	case "note":
		if input.Body.Note == "" {
			return nil, huma.Error400BadRequest("note is required for note action")
		}
		if err := r.repo.AppendMessageEvent(ctx, db.MessageEvent{
			MessageID: input.ID,
			Source:    "operator",
			Type:      "note",
			Summary:   "Operator note added",
			Detail:    input.Body.Note,
		}); err != nil {
			return nil, huma.Error500InternalServerError("failed to append operator note", err)
		}
	case "requeue":
		if err := r.repo.UpdateMessageStatus(ctx, input.ID, message.StatusQueued); err != nil {
			return nil, huma.Error500InternalServerError("failed to requeue message", err)
		}
		if err := r.repo.AppendMessageEvent(ctx, db.MessageEvent{
			MessageID: input.ID,
			Source:    "operator",
			Type:      "requeue",
			Summary:   "Operator requeue requested",
			Detail:    input.Body.Note,
		}); err != nil {
			return nil, huma.Error500InternalServerError("failed to append operator requeue event", err)
		}
		msg.Status = message.StatusQueued
	default:
		return nil, huma.Error400BadRequest("unsupported message action")
	}

	return &messageOutput{Body: toAPIMessage(*msg)}, nil
}

func (r *Router) deleteStoredMessageContent(ctx context.Context, msg message.Message) {
	if r.store == nil {
		return
	}
	seen := map[string]struct{}{}
	for _, key := range []string{msg.ContentPath, msg.StoreID} {
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if err := r.store.Delete(ctx, key); err != nil && !errors.Is(err, fs.ErrNotExist) {
			continue
		}
	}
}

func toAPIMessage(msg message.Message) apiMessage {
	return apiMessage{
		ID:             msg.ID,
		TransactionID:  msg.TransactionID,
		Status:         int16(msg.Status),
		Direction:      int16(msg.Direction),
		From:           msg.From,
		To:             append([]string(nil), msg.To...),
		CC:             append([]string(nil), msg.CC...),
		BCC:            append([]string(nil), msg.BCC...),
		Subject:        msg.Subject,
		ContentType:    msg.ContentType,
		MMSVersion:     msg.MMSVersion,
		DeliveryReport: msg.DeliveryReport,
		ReadReport:     msg.ReadReport,
		MessageSize:    msg.MessageSize,
		ContentPath:    msg.ContentPath,
		StoreID:        msg.StoreID,
		Origin:         int16(msg.Origin),
		OriginHost:     msg.OriginHost,
		ReceivedAt:     formatTime(msg.ReceivedAt),
		UpdatedAt:      formatTime(msg.UpdatedAt),
		Expiry:         formatTimePtr(msg.Expiry),
		DeliveryTime:   formatTimePtr(msg.DeliveryTime),
	}
}

func toAPIMessageEvent(event db.MessageEvent) apiMessageEvent {
	return apiMessageEvent{
		ID:        event.ID,
		MessageID: event.MessageID,
		Source:    event.Source,
		Type:      event.Type,
		Summary:   event.Summary,
		Detail:    event.Detail,
		CreatedAt: formatNullTime(event.CreatedAt),
	}
}

func toAPISMPPSubmission(item db.SMPPSubmission) apiSMPPSubmission {
	return apiSMPPSubmission{
		UpstreamName:      item.UpstreamName,
		SMPPMessageID:     item.SMPPMessageID,
		InternalMessageID: item.InternalMessageID,
		Recipient:         item.Recipient,
		SegmentIndex:      item.SegmentIndex,
		SegmentCount:      item.SegmentCount,
		State:             int(item.State),
		ErrorText:         item.ErrorText,
		SubmittedAt:       formatNullTime(item.SubmittedAt),
		CompletedAt:       formatNullTime(item.CompletedAt),
	}
}

func formatNullTime(value sql.NullTime) string {
	if !value.Valid {
		return ""
	}
	return value.Time.UTC().Format(time.RFC3339)
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func formatTimePtr(value *time.Time) *string {
	if value == nil || value.IsZero() {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339)
	return &formatted
}

func (r *Router) listPeers(ctx context.Context, _ *struct{}) (*peersOutput, error) {
	items, err := r.repo.ListMM4Peers(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list peers", err)
	}
	resp := &peersOutput{}
	resp.Body.Peers = items
	return resp, nil
}

func (r *Router) postPeer(ctx context.Context, input *peerInput) (*peerOutput, error) {
	if err := r.repo.UpsertMM4Peer(ctx, input.Body); err != nil {
		return nil, huma.Error400BadRequest(err.Error(), err)
	}
	return &peerOutput{Body: input.Body}, nil
}

func (r *Router) putPeer(ctx context.Context, input *peerPathInput) (*peerOutput, error) {
	input.Body.Domain = input.Domain
	if err := r.repo.UpsertMM4Peer(ctx, input.Body); err != nil {
		return nil, huma.Error400BadRequest(err.Error(), err)
	}
	return &peerOutput{Body: input.Body}, nil
}

func (r *Router) deletePeer(ctx context.Context, input *peerDeleteInput) (*struct{}, error) {
	if err := r.repo.DeleteMM4Peer(ctx, input.Domain); err != nil {
		return nil, huma.Error400BadRequest(err.Error(), err)
	}
	return &struct{}{}, nil
}

func (r *Router) getMM3Relay(ctx context.Context, _ *struct{}) (*mm3RelayOutput, error) {
	relay, err := r.repo.GetMM3Relay(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to load mm3 relay", err)
	}
	if relay == nil {
		relay = &db.MM3Relay{}
	}
	return &mm3RelayOutput{Body: *relay}, nil
}

func (r *Router) putMM3Relay(ctx context.Context, input *mm3RelayInput) (*mm3RelayOutput, error) {
	if err := r.repo.UpsertMM3Relay(ctx, input.Body); err != nil {
		return nil, huma.Error400BadRequest(err.Error(), err)
	}
	return &mm3RelayOutput{Body: input.Body}, nil
}

func (r *Router) listVASPs(ctx context.Context, _ *struct{}) (*vaspsOutput, error) {
	items, err := r.repo.ListMM7VASPs(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list vasps", err)
	}
	resp := &vaspsOutput{}
	resp.Body.VASPs = items
	return resp, nil
}

func (r *Router) postVASP(ctx context.Context, input *vaspInput) (*vaspOutput, error) {
	if err := r.repo.UpsertMM7VASP(ctx, input.Body); err != nil {
		return nil, huma.Error400BadRequest(err.Error(), err)
	}
	return &vaspOutput{Body: input.Body}, nil
}

func (r *Router) putVASP(ctx context.Context, input *vaspPathInput) (*vaspOutput, error) {
	input.Body.VASPID = input.VASPID
	if err := r.repo.UpsertMM7VASP(ctx, input.Body); err != nil {
		return nil, huma.Error400BadRequest(err.Error(), err)
	}
	return &vaspOutput{Body: input.Body}, nil
}

func (r *Router) deleteVASP(ctx context.Context, input *vaspDeleteInput) (*struct{}, error) {
	if err := r.repo.DeleteMM7VASP(ctx, input.VASPID); err != nil {
		return nil, huma.Error400BadRequest(err.Error(), err)
	}
	return &struct{}{}, nil
}

func (r *Router) listSMPPUpstreams(ctx context.Context, _ *struct{}) (*smppUpstreamsOutput, error) {
	items, err := r.repo.ListSMPPUpstreams(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list smpp upstreams", err)
	}
	resp := &smppUpstreamsOutput{}
	resp.Body.Upstreams = items
	return resp, nil
}

func (r *Router) postSMPPUpstream(ctx context.Context, input *smppUpstreamInput) (*smppUpstreamOutput, error) {
	if err := r.repo.UpsertSMPPUpstream(ctx, input.Body); err != nil {
		return nil, huma.Error400BadRequest(err.Error(), err)
	}
	return &smppUpstreamOutput{Body: input.Body}, nil
}

func (r *Router) putSMPPUpstream(ctx context.Context, input *smppUpstreamPathInput) (*smppUpstreamOutput, error) {
	input.Body.Name = input.Name
	if err := r.repo.UpsertSMPPUpstream(ctx, input.Body); err != nil {
		return nil, huma.Error400BadRequest(err.Error(), err)
	}
	return &smppUpstreamOutput{Body: input.Body}, nil
}

func (r *Router) deleteSMPPUpstream(ctx context.Context, input *smppUpstreamDeleteInput) (*struct{}, error) {
	if err := r.repo.DeleteSMPPUpstream(ctx, input.Name); err != nil {
		return nil, huma.Error400BadRequest(err.Error(), err)
	}
	return &struct{}{}, nil
}

func (r *Router) getRuntime(context.Context, *struct{}) (*runtimeOutput, error) {
	snapshot := r.runtime.Snapshot()
	return &runtimeOutput{
		Body: map[string]any{
			"peers":          snapshot.Peers,
			"mm3_relay":      snapshot.MM3Relay,
			"vasps":          snapshot.VASPs,
			"smpp_upstreams": snapshot.SMPPUpstreams,
			"adaptation":     snapshot.Adaptation,
		},
	}, nil
}

func (r *Router) getSMPPStatus(context.Context, *struct{}) (*smppStatusOutput, error) {
	return &smppStatusOutput{
		Body: map[string]any{
			"upstreams": r.smppManager.Statuses(),
		},
	}, nil
}

func (r *Router) getSystemStatus(ctx context.Context, _ *struct{}) (*systemStatusOutput, error) {
	items, err := r.repo.ListMessages(ctx, db.MessageFilter{Limit: 5000})
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to build system status", err)
	}
	counts := map[string]int{
		"queued":      0,
		"delivering":  0,
		"delivered":   0,
		"expired":     0,
		"rejected":    0,
		"forwarded":   0,
		"unreachable": 0,
	}
	for _, item := range items {
		switch item.Status {
		case message.StatusQueued:
			counts["queued"]++
		case message.StatusDelivering:
			counts["delivering"]++
		case message.StatusDelivered:
			counts["delivered"]++
		case message.StatusExpired:
			counts["expired"]++
		case message.StatusRejected:
			counts["rejected"]++
		case message.StatusForwarded:
			counts["forwarded"]++
		case message.StatusUnreachable:
			counts["unreachable"]++
		}
	}
	queueVisible := counts["queued"] + counts["delivering"]
	return &systemStatusOutput{
		Body: map[string]any{
			"version":        r.version,
			"uptime":         time.Since(r.startedAt).Round(time.Second).String(),
			"started_at":     r.startedAt.UTC().Format(time.RFC3339),
			"queue_visible":  queueVisible,
			"message_counts": counts,
		},
	}, nil
}

func (r *Router) getSystemConfig(context.Context, *struct{}) (*systemConfigOutput, error) {
	return &systemConfigOutput{
		Body: map[string]any{
			"adapt_enabled":          r.cfg.Adapt.Enabled,
			"max_message_size_bytes": r.cfg.Limits.MaxMessageSizeBytes,
		},
	}, nil
}

func (r *Router) listAdaptationClasses(ctx context.Context, _ *struct{}) (*adaptationClassesOutput, error) {
	items, err := r.repo.ListAdaptationClasses(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to list adaptation classes", err)
	}
	resp := &adaptationClassesOutput{}
	resp.Body.Classes = items
	return resp, nil
}

func (r *Router) postAdaptationClass(ctx context.Context, input *adaptationClassInput) (*adaptationClassOutput, error) {
	if err := r.repo.UpsertAdaptationClass(ctx, input.Body); err != nil {
		return nil, huma.Error400BadRequest(err.Error(), err)
	}
	return &adaptationClassOutput{Body: input.Body}, nil
}

func (r *Router) putAdaptationClass(ctx context.Context, input *adaptationClassPathInput) (*adaptationClassOutput, error) {
	input.Body.Name = input.Name
	if err := r.repo.UpsertAdaptationClass(ctx, input.Body); err != nil {
		return nil, huma.Error400BadRequest(err.Error(), err)
	}
	return &adaptationClassOutput{Body: input.Body}, nil
}

func (r *Router) deleteAdaptationClass(ctx context.Context, input *adaptationClassDeleteInput) (*struct{}, error) {
	if err := r.repo.DeleteAdaptationClass(ctx, input.Name); err != nil {
		return nil, huma.Error400BadRequest(err.Error(), err)
	}
	return &struct{}{}, nil
}

func parseMessageFilterValues(limit int, rawStatus string, rawDirection string) (db.MessageFilter, error) {
	filter := db.MessageFilter{Limit: limit}
	if filter.Limit < 0 {
		return db.MessageFilter{}, errInvalidQuery("limit")
	}
	if rawStatus != "" {
		status, ok := parseStatus(rawStatus)
		if !ok {
			return db.MessageFilter{}, errInvalidQuery("status")
		}
		filter.Status = &status
	}
	if rawDirection != "" {
		direction, ok := parseDirection(rawDirection)
		if !ok {
			return db.MessageFilter{}, errInvalidQuery("direction")
		}
		filter.Direction = &direction
	}
	return filter, nil
}

func parseStatus(value string) (message.Status, bool) {
	switch value {
	case "queued":
		return message.StatusQueued, true
	case "delivering":
		return message.StatusDelivering, true
	case "delivered":
		return message.StatusDelivered, true
	case "expired":
		return message.StatusExpired, true
	case "rejected":
		return message.StatusRejected, true
	case "forwarded":
		return message.StatusForwarded, true
	case "unreachable":
		return message.StatusUnreachable, true
	default:
		return 0, false
	}
}

func parseDirection(value string) (message.Direction, bool) {
	switch value {
	case "mo":
		return message.DirectionMO, true
	case "mt":
		return message.DirectionMT, true
	default:
		return 0, false
	}
}

func errInvalidQuery(name string) error {
	return &queryError{name: name}
}

type queryError struct {
	name string
}

func (e *queryError) Error() string {
	return "invalid query parameter: " + e.name
}
