package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vectorcore/vectorcore-mmsc/internal/message"
)

type sqlRepository struct {
	driver string
	db     *sql.DB
}

func (r *sqlRepository) Driver() string {
	return r.driver
}

func (r *sqlRepository) DB() *sql.DB {
	return r.db
}

func (r *sqlRepository) Ping(ctx context.Context) error {
	return r.db.PingContext(ctx)
}

func (r *sqlRepository) Close() error {
	return r.db.Close()
}

func (r *sqlRepository) CreateMessage(ctx context.Context, msg *message.Message) error {
	if msg == nil {
		return errors.New("message is nil")
	}
	if msg.ID == "" {
		msg.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if msg.ReceivedAt.IsZero() {
		msg.ReceivedAt = now
	}
	msg.UpdatedAt = now

	query := r.createMessageSQL()
	_, err := r.db.ExecContext(
		ctx,
		query,
		msg.ID,
		msg.TransactionID,
		int16(msg.Status),
		int16(msg.Direction),
		int16(msg.Origin),
		msg.From,
		r.encodeList(msg.To),
		r.encodeListOrNull(msg.CC),
		r.encodeListOrNull(msg.BCC),
		nullableString(msg.Subject),
		nullableString(msg.ContentType),
		int16(msg.MessageClass),
		int16(msg.Priority),
		defaultString(msg.MMSVersion, "1.3"),
		msg.DeliveryReport,
		msg.ReadReport,
		r.timeValue(msg.Expiry),
		r.timeValue(msg.DeliveryTime),
		msg.MessageSize,
		nullableString(msg.ContentPath),
		nullableString(msg.StoreID),
		nullableString(msg.OriginHost),
		nil,
		r.timeValue(&msg.ReceivedAt),
		r.timeValue(&msg.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("insert message: %w", err)
	}
	_ = r.AppendMessageEvent(ctx, MessageEvent{
		MessageID: msg.ID,
		Source:    "message",
		Type:      "created",
		Summary:   "Message created",
		Detail:    fmt.Sprintf("origin=%d direction=%d from=%s", msg.Origin, msg.Direction, msg.From),
	})
	return nil
}

func (r *sqlRepository) GetMessage(ctx context.Context, id string) (*message.Message, error) {
	row := r.db.QueryRowContext(ctx, r.selectMessageByIDSQL(), id)
	msg, err := r.scanMessage(row)
	if err != nil {
		return nil, err
	}
	return msg, nil
}

func (r *sqlRepository) GetMessageByTransactionID(ctx context.Context, transactionID string) (*message.Message, error) {
	row := r.db.QueryRowContext(ctx, r.selectMessageByTransactionIDSQL(), transactionID)
	msg, err := r.scanMessage(row)
	if err != nil {
		return nil, err
	}
	return msg, nil
}

func (r *sqlRepository) ListMessages(ctx context.Context, filter MessageFilter) ([]message.Message, error) {
	query, args := r.listMessagesSQL(filter)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	var out []message.Message
	for rows.Next() {
		msg, err := r.scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}
	return out, nil
}

// ListMessagesAfter returns messages received after the given watermark
// timestamp, ordered by received_at ascending, up to limit rows.
func (r *sqlRepository) ListMessagesAfter(ctx context.Context, after time.Time, limit int) ([]message.Message, error) {
	query := r.baseSelectMessagesSQL() +
		` where received_at > ` + r.placeholder(1) +
		` order by received_at asc limit ` + r.placeholder(2)
	rows, err := r.db.QueryContext(ctx, query, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list messages after: %w", err)
	}
	defer rows.Close()
	var out []message.Message
	for rows.Next() {
		msg, err := r.scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *msg)
	}
	return out, rows.Err()
}

// ListExpiredMessages returns all messages whose expiry has passed. The expiry
// column is the single point of truth — it is always set at ingestion time by
// ApplyDefaultExpiry, capped to the configured max retention ceiling.
func (r *sqlRepository) ListExpiredMessages(ctx context.Context, now time.Time) ([]message.Message, error) {
	query := r.baseSelectMessagesSQL() + ` where expiry is not null and expiry < ` + r.placeholder(1)
	rows, err := r.db.QueryContext(ctx, query, now)
	if err != nil {
		return nil, fmt.Errorf("list expired messages: %w", err)
	}
	defer rows.Close()

	var out []message.Message
	for rows.Next() {
		msg, err := r.scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *msg)
	}
	return out, rows.Err()
}

func (r *sqlRepository) DeleteMessage(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("message id is required")
	}
	if _, err := r.db.ExecContext(ctx, `delete from delivery_records where message_id = `+r.placeholder(1), id); err != nil {
		return fmt.Errorf("delete delivery records: %w", err)
	}
	result, err := r.db.ExecContext(ctx, `delete from messages where id = `+r.placeholder(1), id)
	if err != nil {
		return fmt.Errorf("delete message: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete message rows affected: %w", err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *sqlRepository) UpdateMessageStatus(ctx context.Context, id string, status message.Status) error {
	_, err := r.db.ExecContext(ctx, `update messages set status = `+r.placeholder(1)+`, updated_at = `+r.nowExpr()+` where id = `+r.placeholder(2), int16(status), id)
	if err != nil {
		return fmt.Errorf("update message status: %w", err)
	}
	_ = r.AppendMessageEvent(ctx, MessageEvent{
		MessageID: id,
		Source:    "message",
		Type:      "status",
		Summary:   "Status updated",
		Detail:    fmt.Sprintf("status=%d", status),
	})
	return nil
}

func (r *sqlRepository) UpdateMessageContent(ctx context.Context, id string, update MessageContentUpdate) error {
	_, err := r.db.ExecContext(
		ctx,
		`update messages set subject = `+r.placeholder(1)+`, content_type = `+r.placeholder(2)+`, message_size = `+r.placeholder(3)+`, content_path = `+r.placeholder(4)+`, store_id = `+r.placeholder(5)+`, updated_at = `+r.nowExpr()+` where id = `+r.placeholder(6),
		nullableString(update.Subject),
		nullableString(update.ContentType),
		update.MessageSize,
		nullableString(update.ContentPath),
		nullableString(update.StoreID),
		id,
	)
	if err != nil {
		return fmt.Errorf("update message content: %w", err)
	}
	_ = r.AppendMessageEvent(ctx, MessageEvent{
		MessageID: id,
		Source:    "message",
		Type:      "content",
		Summary:   "Content metadata updated",
		Detail:    fmt.Sprintf("content_type=%s message_size=%d", update.ContentType, update.MessageSize),
	})
	return nil
}

func (r *sqlRepository) AppendMessageEvent(ctx context.Context, event MessageEvent) error {
	if event.MessageID == "" || event.Source == "" || event.Type == "" || event.Summary == "" {
		return errors.New("message event message_id, source, type, and summary are required")
	}
	_, err := r.db.ExecContext(
		ctx,
		`insert into message_events (message_id, source, event_type, summary, detail, created_at)
		 values (`+r.placeholder(1)+`, `+r.placeholder(2)+`, `+r.placeholder(3)+`, `+r.placeholder(4)+`, `+r.placeholder(5)+`, coalesce(`+r.placeholder(6)+`, `+r.nowExpr()+`))`,
		event.MessageID,
		event.Source,
		event.Type,
		event.Summary,
		nullableString(event.Detail),
		r.timeValue(nullTimePtr(event.CreatedAt)),
	)
	if err != nil {
		return fmt.Errorf("append message event: %w", err)
	}
	if r.driver == "postgres" {
		if _, err := r.db.ExecContext(ctx, `delete from message_events
			where message_id = $1
			  and id in (
			    select id from message_events
			    where message_id = $1
			    order by created_at desc, id desc
			    offset $2
			  )`, event.MessageID, 100); err != nil {
			return fmt.Errorf("prune message events: %w", err)
		}
	} else {
		if _, err := r.db.ExecContext(ctx, `delete from message_events
			where message_id = ?
			  and id in (
			    select id from message_events
			    where message_id = ?
			    order by created_at desc, id desc
			    limit -1 offset ?
			  )`, event.MessageID, event.MessageID, 100); err != nil {
			return fmt.Errorf("prune message events: %w", err)
		}
	}
	return nil
}

func (r *sqlRepository) ListMessageEvents(ctx context.Context, messageID string, limit int) ([]MessageEvent, error) {
	if messageID == "" {
		return nil, errors.New("message event message_id is required")
	}
	if limit <= 0 {
		limit = 100
	}
	var (
		rows *sql.Rows
		err  error
	)
	if r.driver == "postgres" {
		rows, err = r.db.QueryContext(ctx, `select id, message_id, source, event_type, summary, coalesce(detail, ''), created_at
			from message_events
			where message_id = $1
			order by created_at desc, id desc
			limit $2`, messageID, limit)
	} else {
		rows, err = r.db.QueryContext(ctx, `select id, message_id, source, event_type, summary, coalesce(detail, ''), created_at
			from message_events
			where message_id = ?
			order by created_at desc, id desc
			limit ?`, messageID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list message events: %w", err)
	}
	defer rows.Close()
	var out []MessageEvent
	for rows.Next() {
		item, err := r.scanMessageEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate message events: %w", err)
	}
	return out, nil
}

func (r *sqlRepository) UpsertSubscriber(ctx context.Context, sub Subscriber) error {
	if sub.MSISDN == "" {
		return errors.New("subscriber msisdn is required")
	}
	if sub.AdaptationClass == "" {
		sub.AdaptationClass = "default"
	}
	if sub.MaxMessageSize == 0 {
		sub.MaxMessageSize = 307200
	}
	_, err := r.db.ExecContext(
		ctx,
		r.upsertSubscriberSQL(),
		sub.MSISDN,
		sub.Enabled,
		sub.AdaptationClass,
		sub.MaxMessageSize,
		nullableString(sub.HomeMMSC),
	)
	if err != nil {
		return fmt.Errorf("upsert subscriber: %w", err)
	}
	return nil
}

func (r *sqlRepository) GetSubscriber(ctx context.Context, msisdn string) (*Subscriber, error) {
	row := r.db.QueryRowContext(ctx, r.selectSubscriberByMSISDNSQL(), msisdn)
	sub, err := scanSubscriber(row)
	if err != nil {
		return nil, err
	}
	return sub, nil
}

func (r *sqlRepository) ListSubscribers(ctx context.Context) ([]Subscriber, error) {
	rows, err := r.db.QueryContext(ctx, `select msisdn, enabled, adaptation_class, max_msg_size, coalesce(home_mmsc, '') from subscribers order by msisdn`)
	if err != nil {
		return nil, fmt.Errorf("list subscribers: %w", err)
	}
	defer rows.Close()

	var out []Subscriber
	for rows.Next() {
		sub, err := scanSubscriber(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate subscribers: %w", err)
	}
	return out, nil
}

func (r *sqlRepository) UpsertMM4Peer(ctx context.Context, peer MM4Peer) error {
	if peer.Domain == "" || peer.SMTPHost == "" {
		return errors.New("mm4 peer domain and smtp_host are required")
	}
	if peer.Name == "" {
		peer.Name = peer.Domain
	}
	if peer.SMTPPort == 0 {
		peer.SMTPPort = 25
	}
	_, err := r.db.ExecContext(ctx, r.upsertMM4PeerSQL(),
		peer.Name,
		peer.Domain,
		peer.SMTPHost,
		peer.SMTPPort,
		peer.SMTPAuth,
		nullableString(peer.SMTPUser),
		nullableString(peer.SMTPPass),
		peer.TLSEnabled,
		r.encodeListOrNull(peer.AllowedIPs),
		peer.Active,
	)
	if err != nil {
		return fmt.Errorf("upsert mm4 peer: %w", err)
	}
	return nil
}

func (r *sqlRepository) ListMM4Peers(ctx context.Context) ([]MM4Peer, error) {
	rows, err := r.db.QueryContext(ctx, r.listMM4PeersSQL())
	if err != nil {
		return nil, fmt.Errorf("list mm4 peers: %w", err)
	}
	defer rows.Close()

	var out []MM4Peer
	for rows.Next() {
		peer, err := r.scanMM4Peer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *peer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mm4 peers: %w", err)
	}
	return out, nil
}

func (r *sqlRepository) UpsertMM4Route(ctx context.Context, route MM4Route) error {
	if route.Name == "" || route.MatchType == "" || route.MatchValue == "" {
		return errors.New("routing rule name, match_type, and match_value are required")
	}
	switch route.MatchType {
	case "msisdn_exact", "msisdn_prefix", "recipient_domain":
	default:
		return fmt.Errorf("unsupported routing rule match_type: %s", route.MatchType)
	}
	if route.EgressType == "" {
		route.EgressType = "mm4"
	}
	if route.EgressTarget == "" && route.EgressPeerDomain != "" {
		route.EgressTarget = route.EgressPeerDomain
	}
	switch route.EgressType {
	case "local", "reject", "mm3":
		route.EgressTarget = ""
	case "mm4":
		if route.EgressTarget == "" {
			return errors.New("routing rule egress_target is required for mm4 egress")
		}
	default:
		return fmt.Errorf("unsupported routing rule egress_type: %s", route.EgressType)
	}
	var err error
	if route.ID > 0 {
		_, err = r.db.ExecContext(ctx, r.updateMM4RouteSQL(),
			route.Name, route.MatchType, route.MatchValue, route.EgressType, nullableString(route.EgressTarget), route.Priority, route.Active, route.ID)
	} else {
		_, err = r.db.ExecContext(ctx, r.insertMM4RouteSQL(),
			route.Name, route.MatchType, route.MatchValue, route.EgressType, nullableString(route.EgressTarget), route.Priority, route.Active)
	}
	if err != nil {
		return fmt.Errorf("upsert routing rule: %w", err)
	}
	return nil
}

func (r *sqlRepository) ListMM4Routes(ctx context.Context) ([]MM4Route, error) {
	rows, err := r.db.QueryContext(ctx, r.listMM4RoutesSQL())
	if err != nil {
		return nil, fmt.Errorf("list routing rules: %w", err)
	}
	defer rows.Close()

	var out []MM4Route
	for rows.Next() {
		var route MM4Route
		if err := rows.Scan(&route.ID, &route.Name, &route.MatchType, &route.MatchValue, &route.EgressType, &route.EgressTarget, &route.Priority, &route.Active); err != nil {
			return nil, err
		}
		if route.EgressType == "mm4" {
			route.EgressPeerDomain = route.EgressTarget
		}
		out = append(out, route)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate routing rules: %w", err)
	}
	return out, nil
}

func (r *sqlRepository) DeleteMM4Route(ctx context.Context, id int64) error {
	if id <= 0 {
		return errors.New("mm4 route id is required")
	}
	if _, err := r.db.ExecContext(ctx, `delete from routing_rules where id = `+r.placeholder(1), id); err != nil {
		return fmt.Errorf("delete routing rule: %w", err)
	}
	return nil
}

func (r *sqlRepository) DeleteMM4Peer(ctx context.Context, domain string) error {
	if domain == "" {
		return errors.New("mm4 peer domain is required")
	}
	if _, err := r.db.ExecContext(ctx, `delete from mm4_peers where domain = `+r.placeholder(1), domain); err != nil {
		return fmt.Errorf("delete mm4 peer: %w", err)
	}
	return nil
}

func (r *sqlRepository) UpsertMM3Relay(ctx context.Context, relay MM3Relay) error {
	if relay.Enabled && relay.SMTPHost == "" {
		return errors.New("mm3 relay smtp_host is required when enabled")
	}
	if relay.SMTPPort == 0 {
		relay.SMTPPort = 25
	}
	_, err := r.db.ExecContext(ctx, r.upsertMM3RelaySQL(),
		relay.Enabled,
		nullableString(relay.SMTPHost),
		relay.SMTPPort,
		relay.SMTPAuth,
		nullableString(relay.SMTPUser),
		nullableString(relay.SMTPPass),
		relay.TLSEnabled,
		nullableString(relay.DefaultSenderDomain),
		nullableString(relay.DefaultFromAddress),
	)
	if err != nil {
		return fmt.Errorf("upsert mm3 relay: %w", err)
	}
	return nil
}

func (r *sqlRepository) GetMM3Relay(ctx context.Context) (*MM3Relay, error) {
	row := r.db.QueryRowContext(ctx, r.selectMM3RelaySQL())
	var relay MM3Relay
	err := row.Scan(
		&relay.Enabled,
		&relay.SMTPHost,
		&relay.SMTPPort,
		&relay.SMTPAuth,
		&relay.SMTPUser,
		&relay.SMTPPass,
		&relay.TLSEnabled,
		&relay.DefaultSenderDomain,
		&relay.DefaultFromAddress,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get mm3 relay: %w", err)
	}
	return &relay, nil
}

func (r *sqlRepository) UpsertMM7VASP(ctx context.Context, vasp MM7VASP) error {
	if vasp.VASPID == "" {
		return errors.New("mm7 vasp_id is required")
	}
	if vasp.Protocol == "" {
		vasp.Protocol = "soap"
	}
	if vasp.MaxMsgSize == 0 {
		vasp.MaxMsgSize = 1048576
	}
	_, err := r.db.ExecContext(ctx, r.upsertMM7VASPSQL(),
		vasp.VASPID,
		nullableString(vasp.VASID),
		vasp.Protocol,
		nullableString(vasp.Version),
		nullableString(vasp.SharedSecret),
		r.encodeListOrNull(vasp.AllowedIPs),
		nullableString(vasp.DeliverURL),
		nullableString(vasp.ReportURL),
		vasp.MaxMsgSize,
		vasp.Active,
	)
	if err != nil {
		return fmt.Errorf("upsert mm7 vasp: %w", err)
	}
	return nil
}

func (r *sqlRepository) ListMM7VASPs(ctx context.Context) ([]MM7VASP, error) {
	rows, err := r.db.QueryContext(ctx, r.listMM7VASPsSQL())
	if err != nil {
		return nil, fmt.Errorf("list mm7 vasps: %w", err)
	}
	defer rows.Close()

	var out []MM7VASP
	for rows.Next() {
		vasp, err := r.scanMM7VASP(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *vasp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mm7 vasps: %w", err)
	}
	return out, nil
}

func (r *sqlRepository) DeleteMM7VASP(ctx context.Context, vaspID string) error {
	if vaspID == "" {
		return errors.New("mm7 vasp_id is required")
	}
	if _, err := r.db.ExecContext(ctx, `delete from mm7_vasps where vasp_id = `+r.placeholder(1), vaspID); err != nil {
		return fmt.Errorf("delete mm7 vasp: %w", err)
	}
	return nil
}

func (r *sqlRepository) UpsertSMPPUpstream(ctx context.Context, upstream SMPPUpstream) error {
	if upstream.Name == "" || upstream.Host == "" || upstream.SystemID == "" || upstream.Password == "" {
		return errors.New("smpp upstream name, host, system_id, and password are required")
	}
	if upstream.Port == 0 {
		upstream.Port = 2775
	}
	if upstream.BindMode == "" {
		upstream.BindMode = "transceiver"
	}
	if upstream.EnquireLink == 0 {
		upstream.EnquireLink = 30
	}
	if upstream.ReconnectWait == 0 {
		upstream.ReconnectWait = 5
	}
	if upstream.RegisteredDelivery < 0 || upstream.RegisteredDelivery > 3 {
		return errors.New("smpp upstream registered_delivery must be between 0 and 3")
	}
	_, err := r.db.ExecContext(ctx, r.upsertSMPPUpstreamSQL(),
		upstream.Name,
		upstream.Host,
		upstream.Port,
		upstream.SystemID,
		upstream.Password,
		nullableString(upstream.SystemType),
		upstream.BindMode,
		upstream.EnquireLink,
		upstream.ReconnectWait,
		upstream.RegisteredDelivery,
		upstream.Active,
	)
	if err != nil {
		return fmt.Errorf("upsert smpp upstream: %w", err)
	}
	return nil
}

func (r *sqlRepository) ListSMPPUpstreams(ctx context.Context) ([]SMPPUpstream, error) {
	rows, err := r.db.QueryContext(ctx, `select name, host, port, system_id, password, coalesce(system_type, ''), bind_mode, enquire_link, reconnect_wait, registered_delivery, active from smpp_upstream order by name`)
	if err != nil {
		return nil, fmt.Errorf("list smpp upstreams: %w", err)
	}
	defer rows.Close()

	var out []SMPPUpstream
	for rows.Next() {
		var upstream SMPPUpstream
		if err := rows.Scan(
			&upstream.Name,
			&upstream.Host,
			&upstream.Port,
			&upstream.SystemID,
			&upstream.Password,
			&upstream.SystemType,
			&upstream.BindMode,
			&upstream.EnquireLink,
			&upstream.ReconnectWait,
			&upstream.RegisteredDelivery,
			&upstream.Active,
		); err != nil {
			return nil, err
		}
		out = append(out, upstream)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate smpp upstreams: %w", err)
	}
	return out, nil
}

func (r *sqlRepository) DeleteSMPPUpstream(ctx context.Context, name string) error {
	if name == "" {
		return errors.New("smpp upstream name is required")
	}
	if _, err := r.db.ExecContext(ctx, `delete from smpp_upstream where name = `+r.placeholder(1), name); err != nil {
		return fmt.Errorf("delete smpp upstream: %w", err)
	}
	return nil
}

func (r *sqlRepository) CreateSMPPSubmission(ctx context.Context, submission SMPPSubmission) error {
	if submission.UpstreamName == "" || submission.SMPPMessageID == "" || submission.InternalMessageID == "" || submission.Recipient == "" {
		return errors.New("smpp submission upstream_name, smpp_message_id, internal_message_id, and recipient are required")
	}
	if submission.SegmentCount <= 0 {
		submission.SegmentCount = 1
	}
	if !submission.SubmittedAt.Valid {
		submission.SubmittedAt = sql.NullTime{Time: time.Now().UTC(), Valid: true}
	}
	_, err := r.db.ExecContext(
		ctx,
		r.upsertSMPPSubmissionSQL(),
		submission.UpstreamName,
		submission.SMPPMessageID,
		submission.InternalMessageID,
		submission.Recipient,
		submission.SegmentIndex,
		submission.SegmentCount,
		int16(submission.State),
		nullableString(submission.ErrorText),
		r.timeValue(nullTimePtr(submission.SubmittedAt)),
		r.timeValue(nullTimePtr(submission.CompletedAt)),
	)
	if err != nil {
		return fmt.Errorf("create smpp submission: %w", err)
	}
	_ = r.AppendMessageEvent(ctx, MessageEvent{
		MessageID: submission.InternalMessageID,
		Source:    "smpp",
		Type:      "submission",
		Summary:   "SMPP submission created",
		Detail:    fmt.Sprintf("upstream=%s recipient=%s segment=%d/%d remote_id=%s", submission.UpstreamName, submission.Recipient, submission.SegmentIndex+1, submission.SegmentCount, submission.SMPPMessageID),
	})
	return nil
}

func (r *sqlRepository) UpdateSMPPSubmissionReceipt(ctx context.Context, upstreamName string, smppMessageID string, state SMPPSubmissionState, errorText string) (*SMPPSubmission, error) {
	row := r.db.QueryRowContext(
		ctx,
		r.updateSMPPSubmissionReceiptSQL(),
		int16(state),
		nullableString(errorText),
		upstreamName,
		smppMessageID,
	)
	submission, err := r.scanSMPPSubmission(row)
	if err != nil {
		return nil, err
	}
	_ = r.AppendMessageEvent(ctx, MessageEvent{
		MessageID: submission.InternalMessageID,
		Source:    "smpp",
		Type:      "receipt",
		Summary:   "SMPP receipt updated",
		Detail:    fmt.Sprintf("upstream=%s remote_id=%s state=%d error=%s", upstreamName, smppMessageID, state, errorText),
	})
	return submission, nil
}

func (r *sqlRepository) ListSMPPSubmissions(ctx context.Context, internalMessageID string) ([]SMPPSubmission, error) {
	query := `select upstream_name, smpp_message_id, internal_message_id, recipient, segment_index, segment_count, state, coalesce(error_text, ''), submitted_at, completed_at from smpp_submissions`
	args := []any{}
	if internalMessageID != "" {
		query += ` where internal_message_id = ` + r.placeholder(1)
		args = append(args, internalMessageID)
	}
	query += ` order by internal_message_id, upstream_name, segment_index`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list smpp submissions: %w", err)
	}
	defer rows.Close()

	var out []SMPPSubmission
	for rows.Next() {
		item, err := r.scanSMPPSubmission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate smpp submissions: %w", err)
	}
	return out, nil
}

func (r *sqlRepository) UpsertAdaptationClass(ctx context.Context, class AdaptationClass) error {
	if class.Name == "" {
		return errors.New("adaptation class name is required")
	}
	if class.MaxMsgSizeBytes == 0 {
		class.MaxMsgSizeBytes = 307200
	}
	if class.MaxImageWidth == 0 {
		class.MaxImageWidth = 640
	}
	if class.MaxImageHeight == 0 {
		class.MaxImageHeight = 480
	}
	if len(class.AllowedImageTypes) == 0 {
		class.AllowedImageTypes = []string{"image/jpeg", "image/gif", "image/png"}
	}
	if len(class.AllowedAudioTypes) == 0 {
		class.AllowedAudioTypes = []string{"audio/amr", "audio/mpeg", "audio/mp4"}
	}
	if len(class.AllowedVideoTypes) == 0 {
		class.AllowedVideoTypes = []string{"video/3gpp", "video/mp4"}
	}
	_, err := r.db.ExecContext(ctx, r.upsertAdaptationClassSQL(),
		class.Name,
		class.MaxMsgSizeBytes,
		class.MaxImageWidth,
		class.MaxImageHeight,
		r.encodeList(class.AllowedImageTypes),
		r.encodeList(class.AllowedAudioTypes),
		r.encodeList(class.AllowedVideoTypes),
	)
	if err != nil {
		return fmt.Errorf("upsert adaptation class: %w", err)
	}
	return nil
}

func (r *sqlRepository) GetAdaptationClass(ctx context.Context, name string) (*AdaptationClass, error) {
	row := r.db.QueryRowContext(ctx, r.selectAdaptationClassByNameSQL(), name)
	return scanAdaptationClass(row, r.driver)
}

func (r *sqlRepository) ListAdaptationClasses(ctx context.Context) ([]AdaptationClass, error) {
	rows, err := r.db.QueryContext(ctx, r.listAdaptationClassesSQL())
	if err != nil {
		return nil, fmt.Errorf("list adaptation classes: %w", err)
	}
	defer rows.Close()

	var out []AdaptationClass
	for rows.Next() {
		item, err := scanAdaptationClass(rows, r.driver)
		if err != nil {
			return nil, err
		}
		out = append(out, *item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate adaptation classes: %w", err)
	}
	return out, nil
}

func (r *sqlRepository) DeleteAdaptationClass(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("adaptation class name is required")
	}
	if _, err := r.db.ExecContext(ctx, r.deleteAdaptationClassSQL(), name); err != nil {
		return fmt.Errorf("delete adaptation class: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func (r *sqlRepository) scanMessage(scanner rowScanner) (*message.Message, error) {
	msg := &message.Message{}
	var (
		status, direction, origin, class, priority int16
		toCSV, ccCSV, bccCSV                       string
		subject, contentType, contentPath          string
		storeID, originHost, mmsVersion            string
	)

	if r.driver == "sqlite" {
		var expiry, deliveryTime, receivedAt, updatedAt sql.NullString
		if err := scanner.Scan(
			&msg.ID,
			&msg.TransactionID,
			&status,
			&direction,
			&origin,
			&msg.From,
			&toCSV,
			&ccCSV,
			&bccCSV,
			&subject,
			&contentType,
			&class,
			&priority,
			&mmsVersion,
			&msg.DeliveryReport,
			&msg.ReadReport,
			&expiry,
			&deliveryTime,
			&msg.MessageSize,
			&contentPath,
			&storeID,
			&originHost,
			&receivedAt,
			&updatedAt,
		); err != nil {
			return nil, err
		}
		msg.Expiry = parseNullableTimeString(expiry)
		msg.DeliveryTime = parseNullableTimeString(deliveryTime)
		msg.ReceivedAt = parseNullableTimeStringValue(receivedAt)
		msg.UpdatedAt = parseNullableTimeStringValue(updatedAt)
	} else {
		var expiry, deliveryTime, receivedAt, updatedAt sql.NullTime
		if err := scanner.Scan(
			&msg.ID,
			&msg.TransactionID,
			&status,
			&direction,
			&origin,
			&msg.From,
			&toCSV,
			&ccCSV,
			&bccCSV,
			&subject,
			&contentType,
			&class,
			&priority,
			&mmsVersion,
			&msg.DeliveryReport,
			&msg.ReadReport,
			&expiry,
			&deliveryTime,
			&msg.MessageSize,
			&contentPath,
			&storeID,
			&originHost,
			&receivedAt,
			&updatedAt,
		); err != nil {
			return nil, err
		}
		msg.Expiry = nullTimePtr(expiry)
		msg.DeliveryTime = nullTimePtr(deliveryTime)
		if receivedAt.Valid {
			msg.ReceivedAt = receivedAt.Time.UTC()
		}
		if updatedAt.Valid {
			msg.UpdatedAt = updatedAt.Time.UTC()
		}
	}

	msg.Status = message.Status(status)
	msg.Direction = message.Direction(direction)
	msg.Origin = message.Interface(origin)
	msg.To = decodeCSVList(toCSV)
	msg.CC = decodeCSVList(ccCSV)
	msg.BCC = decodeCSVList(bccCSV)
	msg.Subject = subject
	msg.ContentType = contentType
	msg.MessageClass = message.MessageClass(class)
	msg.Priority = message.Priority(priority)
	msg.MMSVersion = mmsVersion
	msg.ContentPath = contentPath
	msg.StoreID = storeID
	msg.OriginHost = originHost
	return msg, nil
}

func (r *sqlRepository) createMessageSQL() string {
	if r.driver == "postgres" {
		return `insert into messages (
			id, transaction_id, status, direction, origin_if, from_addr, to_addrs, cc_addrs, bcc_addrs,
			subject, content_type, message_class, priority, mms_version, delivery_report, read_report,
			expiry, delivery_time, message_size, content_path, store_id, origin_host, vasp_id, received_at, updated_at
		) values (
			$1, $2, $3, $4, $5, $6, string_to_array($7, ','), string_to_array(nullif($8, ''), ','),
			string_to_array(nullif($9, ''), ','), $10, $11, $12, $13, $14, $15, $16, $17, $18, $19,
			$20, $21, $22, $23, $24, $25
		)`
	}
	return `insert into messages (
		id, transaction_id, status, direction, origin_if, from_addr, to_addrs, cc_addrs, bcc_addrs,
		subject, content_type, message_class, priority, mms_version, delivery_report, read_report,
		expiry, delivery_time, message_size, content_path, store_id, origin_host, vasp_id, received_at, updated_at
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
}

func (r *sqlRepository) selectMessageByIDSQL() string {
	return r.baseSelectMessagesSQL() + ` where id = ` + r.placeholder(1)
}

func (r *sqlRepository) selectMessageByTransactionIDSQL() string {
	return r.baseSelectMessagesSQL() + ` where transaction_id = ` + r.placeholder(1) + ` order by received_at desc limit 1`
}

func (r *sqlRepository) listMessagesSQL(filter MessageFilter) (string, []any) {
	query := r.baseSelectMessagesSQL()
	var conditions []string
	var args []any

	if filter.Status != nil {
		args = append(args, int16(*filter.Status))
		conditions = append(conditions, `status = `+r.placeholder(len(args)))
	}
	if filter.Direction != nil {
		args = append(args, int16(*filter.Direction))
		conditions = append(conditions, `direction = `+r.placeholder(len(args)))
	}
	if len(conditions) > 0 {
		query += ` where ` + strings.Join(conditions, ` and `)
	}
	query += ` order by received_at desc`
	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		query += ` limit ` + r.placeholder(len(args))
	}
	return query, args
}

func (r *sqlRepository) baseSelectMessagesSQL() string {
	if r.driver == "postgres" {
		return `select
			id, transaction_id, status, direction, origin_if, from_addr,
			array_to_string(to_addrs, ','), coalesce(array_to_string(cc_addrs, ','), ''),
			coalesce(array_to_string(bcc_addrs, ','), ''), coalesce(subject, ''), coalesce(content_type, ''),
			message_class, priority, mms_version, delivery_report, read_report, expiry, delivery_time,
			message_size, coalesce(content_path, ''), coalesce(store_id, ''), coalesce(origin_host, ''),
			received_at, updated_at
		from messages`
	}
	return `select
		id, transaction_id, status, direction, origin_if, from_addr, to_addrs, coalesce(cc_addrs, ''),
		coalesce(bcc_addrs, ''), coalesce(subject, ''), coalesce(content_type, ''), message_class,
		priority, mms_version, delivery_report, read_report, expiry, delivery_time, message_size,
		coalesce(content_path, ''), coalesce(store_id, ''), coalesce(origin_host, ''), received_at, updated_at
	from messages`
}

func (r *sqlRepository) upsertSubscriberSQL() string {
	if r.driver == "postgres" {
		return `insert into subscribers (msisdn, enabled, adaptation_class, max_msg_size, home_mmsc)
			values ($1, $2, $3, $4, $5)
			on conflict (msisdn) do update set
				enabled = excluded.enabled,
				adaptation_class = excluded.adaptation_class,
				max_msg_size = excluded.max_msg_size,
				home_mmsc = excluded.home_mmsc,
				updated_at = now()`
	}
	return `insert into subscribers (msisdn, enabled, adaptation_class, max_msg_size, home_mmsc)
		values (?, ?, ?, ?, ?)
		on conflict(msisdn) do update set
			enabled = excluded.enabled,
			adaptation_class = excluded.adaptation_class,
			max_msg_size = excluded.max_msg_size,
			home_mmsc = excluded.home_mmsc,
			updated_at = current_timestamp`
}

func (r *sqlRepository) upsertMM4PeerSQL() string {
	if r.driver == "postgres" {
		return `insert into mm4_peers (name, domain, smtp_host, smtp_port, smtp_auth, smtp_user, smtp_pass, tls_enabled, allowed_ips, active)
			values ($1, $2, $3, $4, $5, $6, $7, $8, cast(string_to_array(nullif($9, ''), ',') as inet[]), $10)
			on conflict (domain) do update set
				name = excluded.name,
				smtp_host = excluded.smtp_host,
				smtp_port = excluded.smtp_port,
				smtp_auth = excluded.smtp_auth,
				smtp_user = excluded.smtp_user,
				smtp_pass = excluded.smtp_pass,
				tls_enabled = excluded.tls_enabled,
				allowed_ips = excluded.allowed_ips,
				active = excluded.active,
				updated_at = now()`
	}
	return `insert into mm4_peers (name, domain, smtp_host, smtp_port, smtp_auth, smtp_user, smtp_pass, tls_enabled, allowed_ips, active)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(domain) do update set
			name = excluded.name,
			smtp_host = excluded.smtp_host,
			smtp_port = excluded.smtp_port,
			smtp_auth = excluded.smtp_auth,
			smtp_user = excluded.smtp_user,
			smtp_pass = excluded.smtp_pass,
			tls_enabled = excluded.tls_enabled,
			allowed_ips = excluded.allowed_ips,
			active = excluded.active,
			updated_at = current_timestamp`
}

func (r *sqlRepository) listMM4PeersSQL() string {
	if r.driver == "postgres" {
		return `select coalesce(name, domain), domain, smtp_host, smtp_port, smtp_auth, coalesce(smtp_user, ''), coalesce(smtp_pass, ''), tls_enabled, coalesce(array_to_string(allowed_ips::text[], ','), ''), active from mm4_peers order by domain`
	}
	return `select coalesce(nullif(name, ''), domain), domain, smtp_host, smtp_port, smtp_auth, coalesce(smtp_user, ''), coalesce(smtp_pass, ''), tls_enabled, coalesce(allowed_ips, ''), active from mm4_peers order by domain`
}

func (r *sqlRepository) insertMM4RouteSQL() string {
	if r.driver == "postgres" {
		return `insert into routing_rules (name, match_type, match_value, egress_type, egress_target, priority, active)
			values ($1, $2, $3, $4, $5, $6, $7)`
	}
	return `insert into routing_rules (name, match_type, match_value, egress_type, egress_target, priority, active)
		values (?, ?, ?, ?, ?, ?, ?)`
}

func (r *sqlRepository) updateMM4RouteSQL() string {
	if r.driver == "postgres" {
		return `update routing_rules set name = $1, match_type = $2, match_value = $3, egress_type = $4, egress_target = $5, priority = $6, active = $7, updated_at = now() where id = $8`
	}
	return `update routing_rules set name = ?, match_type = ?, match_value = ?, egress_type = ?, egress_target = ?, priority = ?, active = ?, updated_at = current_timestamp where id = ?`
}

func (r *sqlRepository) listMM4RoutesSQL() string {
	return `select id, name, match_type, match_value, egress_type, coalesce(egress_target, ''), priority, active from routing_rules order by priority desc, length(match_value) desc, id`
}

func (r *sqlRepository) upsertMM7VASPSQL() string {
	if r.driver == "postgres" {
		return `insert into mm7_vasps (vasp_id, vas_id, protocol, version, shared_secret, allowed_ips, deliver_url, report_url, max_msg_size, active)
			values ($1, $2, $3, $4, $5, cast(string_to_array(nullif($6, ''), ',') as inet[]), $7, $8, $9, $10)
			on conflict (vasp_id) do update set
				vas_id = excluded.vas_id,
				protocol = excluded.protocol,
				version = excluded.version,
				shared_secret = excluded.shared_secret,
				allowed_ips = excluded.allowed_ips,
				deliver_url = excluded.deliver_url,
				report_url = excluded.report_url,
				max_msg_size = excluded.max_msg_size,
				active = excluded.active,
				updated_at = now()`
	}
	return `insert into mm7_vasps (vasp_id, vas_id, protocol, version, shared_secret, allowed_ips, deliver_url, report_url, max_msg_size, active)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(vasp_id) do update set
			vas_id = excluded.vas_id,
			protocol = excluded.protocol,
			version = excluded.version,
			shared_secret = excluded.shared_secret,
			allowed_ips = excluded.allowed_ips,
			deliver_url = excluded.deliver_url,
			report_url = excluded.report_url,
			max_msg_size = excluded.max_msg_size,
			active = excluded.active,
			updated_at = current_timestamp`
}

func (r *sqlRepository) upsertMM3RelaySQL() string {
	if r.driver == "postgres" {
		return `insert into mm3_relay (id, enabled, smtp_host, smtp_port, smtp_auth, smtp_user, smtp_pass, tls_enabled, default_sender_domain, default_from_address)
			values (1, $1, coalesce($2, ''), $3, $4, $5, $6, $7, $8, $9)
			on conflict (id) do update set
				enabled = excluded.enabled,
				smtp_host = excluded.smtp_host,
				smtp_port = excluded.smtp_port,
				smtp_auth = excluded.smtp_auth,
				smtp_user = excluded.smtp_user,
				smtp_pass = excluded.smtp_pass,
				tls_enabled = excluded.tls_enabled,
				default_sender_domain = excluded.default_sender_domain,
				default_from_address = excluded.default_from_address,
				updated_at = now()`
	}
	return `insert into mm3_relay (id, enabled, smtp_host, smtp_port, smtp_auth, smtp_user, smtp_pass, tls_enabled, default_sender_domain, default_from_address)
		values (1, ?, coalesce(?, ''), ?, ?, ?, ?, ?, ?, ?)
		on conflict(id) do update set
			enabled = excluded.enabled,
			smtp_host = excluded.smtp_host,
			smtp_port = excluded.smtp_port,
			smtp_auth = excluded.smtp_auth,
			smtp_user = excluded.smtp_user,
			smtp_pass = excluded.smtp_pass,
			tls_enabled = excluded.tls_enabled,
			default_sender_domain = excluded.default_sender_domain,
			default_from_address = excluded.default_from_address,
			updated_at = current_timestamp`
}

func (r *sqlRepository) selectMM3RelaySQL() string {
	return `select enabled, coalesce(smtp_host, ''), smtp_port, smtp_auth, coalesce(smtp_user, ''), coalesce(smtp_pass, ''), tls_enabled, coalesce(default_sender_domain, ''), coalesce(default_from_address, '') from mm3_relay where id = 1`
}

func (r *sqlRepository) listMM7VASPsSQL() string {
	if r.driver == "postgres" {
		return `select vasp_id, coalesce(vas_id, ''), coalesce(protocol, 'soap'), coalesce(version, ''), coalesce(shared_secret, ''), coalesce(array_to_string(allowed_ips::text[], ','), ''), coalesce(deliver_url, ''), coalesce(report_url, ''), max_msg_size, active from mm7_vasps order by vasp_id`
	}
	return `select vasp_id, coalesce(vas_id, ''), coalesce(protocol, 'soap'), coalesce(version, ''), coalesce(shared_secret, ''), coalesce(allowed_ips, ''), coalesce(deliver_url, ''), coalesce(report_url, ''), max_msg_size, active from mm7_vasps order by vasp_id`
}

func (r *sqlRepository) upsertSMPPUpstreamSQL() string {
	if r.driver == "postgres" {
		return `insert into smpp_upstream (name, host, port, system_id, password, system_type, bind_mode, enquire_link, reconnect_wait, registered_delivery, active)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			on conflict (name) do update set
				host = excluded.host,
				port = excluded.port,
				system_id = excluded.system_id,
				password = excluded.password,
				system_type = excluded.system_type,
				bind_mode = excluded.bind_mode,
				enquire_link = excluded.enquire_link,
				reconnect_wait = excluded.reconnect_wait,
				registered_delivery = excluded.registered_delivery,
				active = excluded.active,
				updated_at = now()`
	}
	return `insert into smpp_upstream (name, host, port, system_id, password, system_type, bind_mode, enquire_link, reconnect_wait, registered_delivery, active)
		values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(name) do update set
			host = excluded.host,
			port = excluded.port,
			system_id = excluded.system_id,
			password = excluded.password,
			system_type = excluded.system_type,
			bind_mode = excluded.bind_mode,
			enquire_link = excluded.enquire_link,
			reconnect_wait = excluded.reconnect_wait,
			registered_delivery = excluded.registered_delivery,
			active = excluded.active,
			updated_at = current_timestamp`
}

func (r *sqlRepository) upsertSMPPSubmissionSQL() string {
	if r.driver == "postgres" {
		return `insert into smpp_submissions (
			upstream_name, smpp_message_id, internal_message_id, recipient, segment_index, segment_count, state, error_text, submitted_at, completed_at
		) values (
			$1, $2, $3, $4, $5, $6, $7, $8, coalesce($9, now()), $10
		)
		on conflict (upstream_name, smpp_message_id) do update set
			internal_message_id = excluded.internal_message_id,
			recipient = excluded.recipient,
			segment_index = excluded.segment_index,
			segment_count = excluded.segment_count,
			state = excluded.state,
			error_text = excluded.error_text,
			submitted_at = excluded.submitted_at,
			completed_at = excluded.completed_at`
	}
	return `insert into smpp_submissions (
		upstream_name, smpp_message_id, internal_message_id, recipient, segment_index, segment_count, state, error_text, submitted_at, completed_at
	) values (?, ?, ?, ?, ?, ?, ?, ?, coalesce(?, current_timestamp), ?)
	on conflict(upstream_name, smpp_message_id) do update set
		internal_message_id = excluded.internal_message_id,
		recipient = excluded.recipient,
		segment_index = excluded.segment_index,
		segment_count = excluded.segment_count,
		state = excluded.state,
		error_text = excluded.error_text,
		submitted_at = excluded.submitted_at,
		completed_at = excluded.completed_at`
}

func (r *sqlRepository) updateSMPPSubmissionReceiptSQL() string {
	if r.driver == "postgres" {
		return `update smpp_submissions
			set state = $1, error_text = $2, completed_at = now()
			where upstream_name = $3 and smpp_message_id = $4
			returning upstream_name, smpp_message_id, internal_message_id, recipient, segment_index, segment_count, state, coalesce(error_text, ''), submitted_at, completed_at`
	}
	return `update smpp_submissions
		set state = ?, error_text = ?, completed_at = current_timestamp
		where upstream_name = ? and smpp_message_id = ?
		returning upstream_name, smpp_message_id, internal_message_id, recipient, segment_index, segment_count, state, coalesce(error_text, ''), submitted_at, completed_at`
}

func (r *sqlRepository) upsertAdaptationClassSQL() string {
	if r.driver == "postgres" {
		return `insert into adaptation_classes (name, max_msg_size_bytes, max_image_width, max_image_height, allowed_img_types, allowed_audio_types, allowed_video_types)
			values ($1, $2, $3, $4, string_to_array($5, ','), string_to_array($6, ','), string_to_array($7, ','))
			on conflict (name) do update set
				max_msg_size_bytes = excluded.max_msg_size_bytes,
				max_image_width = excluded.max_image_width,
				max_image_height = excluded.max_image_height,
				allowed_img_types = excluded.allowed_img_types,
				allowed_audio_types = excluded.allowed_audio_types,
				allowed_video_types = excluded.allowed_video_types,
				updated_at = now()`
	}
	return `insert into adaptation_classes (name, max_msg_size_bytes, max_image_width, max_image_height, allowed_img_types, allowed_audio_types, allowed_video_types)
		values (?, ?, ?, ?, ?, ?, ?)
		on conflict(name) do update set
			max_msg_size_bytes = excluded.max_msg_size_bytes,
			max_image_width = excluded.max_image_width,
			max_image_height = excluded.max_image_height,
			allowed_img_types = excluded.allowed_img_types,
			allowed_audio_types = excluded.allowed_audio_types,
			allowed_video_types = excluded.allowed_video_types,
			updated_at = current_timestamp`
}

func (r *sqlRepository) selectAdaptationClassByNameSQL() string {
	if r.driver == "postgres" {
		return `select name, max_msg_size_bytes, max_image_width, max_image_height, array_to_string(allowed_img_types, ','), array_to_string(allowed_audio_types, ','), array_to_string(allowed_video_types, ',') from adaptation_classes where name = ` + r.placeholder(1)
	}
	return `select name, max_msg_size_bytes, max_image_width, max_image_height, allowed_img_types, allowed_audio_types, allowed_video_types from adaptation_classes where name = ` + r.placeholder(1)
}

func (r *sqlRepository) listAdaptationClassesSQL() string {
	if r.driver == "postgres" {
		return `select name, max_msg_size_bytes, max_image_width, max_image_height, array_to_string(allowed_img_types, ','), array_to_string(allowed_audio_types, ','), array_to_string(allowed_video_types, ',') from adaptation_classes order by name`
	}
	return `select name, max_msg_size_bytes, max_image_width, max_image_height, allowed_img_types, allowed_audio_types, allowed_video_types from adaptation_classes order by name`
}

func (r *sqlRepository) deleteAdaptationClassSQL() string {
	return `delete from adaptation_classes where name = ` + r.placeholder(1)
}

func (r *sqlRepository) selectSubscriberByMSISDNSQL() string {
	return `select msisdn, enabled, adaptation_class, max_msg_size, coalesce(home_mmsc, '') from subscribers where msisdn = ` + r.placeholder(1)
}

func (r *sqlRepository) placeholder(n int) string {
	if r.driver == "postgres" {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

func (r *sqlRepository) nowExpr() string {
	if r.driver == "postgres" {
		return "now()"
	}
	return "current_timestamp"
}

func (r *sqlRepository) encodeList(values []string) string {
	return strings.Join(values, ",")
}

func (r *sqlRepository) encodeListOrNull(values []string) any {
	if len(values) == 0 {
		return ""
	}
	return strings.Join(values, ",")
}

func (r *sqlRepository) timeValue(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UTC()
}

func scanSubscriber(scanner rowScanner) (*Subscriber, error) {
	var sub Subscriber
	if err := scanner.Scan(&sub.MSISDN, &sub.Enabled, &sub.AdaptationClass, &sub.MaxMessageSize, &sub.HomeMMSC); err != nil {
		return nil, err
	}
	return &sub, nil
}

func (r *sqlRepository) scanSMPPSubmission(scanner rowScanner) (*SMPPSubmission, error) {
	var (
		item      SMPPSubmission
		state     int16
		errorText string
	)
	if r.driver == "sqlite" {
		var submitted sql.NullString
		var completed sql.NullString
		if err := scanner.Scan(
			&item.UpstreamName,
			&item.SMPPMessageID,
			&item.InternalMessageID,
			&item.Recipient,
			&item.SegmentIndex,
			&item.SegmentCount,
			&state,
			&errorText,
			&submitted,
			&completed,
		); err != nil {
			return nil, err
		}
		if submitted.Valid {
			if parsed := parseTimeString(submitted.String); parsed != nil {
				item.SubmittedAt = sql.NullTime{Time: *parsed, Valid: true}
			}
		}
		if completed.Valid {
			if parsed := parseTimeString(completed.String); parsed != nil {
				item.CompletedAt = sql.NullTime{Time: *parsed, Valid: true}
			}
		}
	} else {
		var submitted sql.NullTime
		var completed sql.NullTime
		if err := scanner.Scan(
			&item.UpstreamName,
			&item.SMPPMessageID,
			&item.InternalMessageID,
			&item.Recipient,
			&item.SegmentIndex,
			&item.SegmentCount,
			&state,
			&errorText,
			&submitted,
			&completed,
		); err != nil {
			return nil, err
		}
		item.SubmittedAt = submitted
		item.CompletedAt = completed
	}
	item.State = SMPPSubmissionState(state)
	item.ErrorText = errorText
	return &item, nil
}

func (r *sqlRepository) scanMessageEvent(scanner rowScanner) (*MessageEvent, error) {
	var item MessageEvent
	var detail string
	if r.driver == "sqlite" {
		var created sql.NullString
		if err := scanner.Scan(&item.ID, &item.MessageID, &item.Source, &item.Type, &item.Summary, &detail, &created); err != nil {
			return nil, fmt.Errorf("scan message event: %w", err)
		}
		if created.Valid {
			if parsed := parseTimeString(created.String); parsed != nil {
				item.CreatedAt = sql.NullTime{Time: *parsed, Valid: true}
			}
		}
	} else {
		if err := scanner.Scan(&item.ID, &item.MessageID, &item.Source, &item.Type, &item.Summary, &detail, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message event: %w", err)
		}
	}
	item.Detail = detail
	return &item, nil
}

func (r *sqlRepository) scanMM4Peer(scanner rowScanner) (*MM4Peer, error) {
	var peer MM4Peer
	var allowedIPs string
	if err := scanner.Scan(
		&peer.Name,
		&peer.Domain,
		&peer.SMTPHost,
		&peer.SMTPPort,
		&peer.SMTPAuth,
		&peer.SMTPUser,
		&peer.SMTPPass,
		&peer.TLSEnabled,
		&allowedIPs,
		&peer.Active,
	); err != nil {
		return nil, err
	}
	peer.AllowedIPs = decodeCSVList(allowedIPs)
	return &peer, nil
}

func (r *sqlRepository) scanMM7VASP(scanner rowScanner) (*MM7VASP, error) {
	var vasp MM7VASP
	var allowedIPs string
	if err := scanner.Scan(
		&vasp.VASPID,
		&vasp.VASID,
		&vasp.Protocol,
		&vasp.Version,
		&vasp.SharedSecret,
		&allowedIPs,
		&vasp.DeliverURL,
		&vasp.ReportURL,
		&vasp.MaxMsgSize,
		&vasp.Active,
	); err != nil {
		return nil, err
	}
	if vasp.Protocol == "" {
		vasp.Protocol = "soap"
	}
	vasp.AllowedIPs = decodeCSVList(allowedIPs)
	return &vasp, nil
}

func scanAdaptationClass(scanner rowScanner, driver string) (*AdaptationClass, error) {
	var class AdaptationClass
	var imgTypes, audioTypes, videoTypes string
	if err := scanner.Scan(
		&class.Name,
		&class.MaxMsgSizeBytes,
		&class.MaxImageWidth,
		&class.MaxImageHeight,
		&imgTypes,
		&audioTypes,
		&videoTypes,
	); err != nil {
		return nil, err
	}
	class.AllowedImageTypes = decodeCSVList(imgTypes)
	class.AllowedAudioTypes = decodeCSVList(audioTypes)
	class.AllowedVideoTypes = decodeCSVList(videoTypes)
	return &class, nil
}

func decodeCSVList(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func nullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time.UTC()
	return &t
}

func parseNullableTimeString(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	return parseTimeString(value.String)
}

func parseNullableTimeStringValue(value sql.NullString) time.Time {
	if !value.Valid || value.String == "" {
		return time.Time{}
	}
	parsed := parseTimeString(value.String)
	if parsed == nil {
		return time.Time{}
	}
	return *parsed
}

func parseTimeString(value string) *time.Time {
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			t := parsed.UTC()
			return &t
		}
	}
	return nil
}
