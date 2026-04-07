package mm1

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/vectorcore/vectorcore-mmsc/internal/adapt"
	"github.com/vectorcore/vectorcore-mmsc/internal/config"
	"github.com/vectorcore/vectorcore-mmsc/internal/db"
	"github.com/vectorcore/vectorcore-mmsc/internal/mm3"
	"github.com/vectorcore/vectorcore-mmsc/internal/mm4"
	"github.com/vectorcore/vectorcore-mmsc/internal/mmspdu"
	"github.com/vectorcore/vectorcore-mmsc/internal/routing"
	"github.com/vectorcore/vectorcore-mmsc/internal/smpp"
	"github.com/vectorcore/vectorcore-mmsc/internal/store"
)

type Server struct {
	cfg     *config.Config
	repo    db.Repository
	store   store.Store
	router  *routing.Engine
	smpp    *smpp.Manager
	handler *MOHandler
	mt      *MTHandler
}

func NewServer(cfg *config.Config, repo db.Repository, contentStore store.Store, router *routing.Engine, smppManager *smpp.Manager, mm4Sender *mm4.Outbound, mm3Sender *mm3.Outbound, reporter MTReporter) *Server {
	pipeline := adapt.NewPipeline(cfg.Adapt, repo)
	return &Server{
		cfg:     cfg,
		repo:    repo,
		store:   contentStore,
		router:  router,
		smpp:    smppManager,
		handler: NewMOHandler(cfg, repo, contentStore, router, smppManager, mm4Sender, mm3Sender, pipeline),
		mt:      NewMTHandler(cfg, repo, contentStore, reporter),
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log := zap.L().With(
		zap.String("interface", "mm1"),
		zap.String("remote", r.RemoteAddr),
		zap.String("method", r.Method),
		zap.String("path", r.URL.RequestURI()),
	)
	if r.Method == http.MethodGet {
		log.Debug("handling mm1 retrieve request")
		s.mt.HandleRetrieve(w, r)
		return
	}

	if r.Method == http.MethodPost && strings.EqualFold(r.Header.Get("Content-Type"), "application/vnd.wap.mms-message") {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MM1.MaxBodySizeBytes))
		if err != nil {
			log.Debug("mm1 request body rejected", zap.Error(err))
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		pdu, err := mmspdu.Decode(data)
		if err != nil {
			log.Debug("mm1 pdu decode failed", zap.Error(err), zap.Int("payload_bytes", len(data)))
			http.Error(w, "invalid mms pdu", http.StatusBadRequest)
			return
		}
		log.Debug("mm1 pdu decoded", zap.Uint8("message_type", pdu.MessageType), zap.String("transaction_id", pdu.TransactionID), zap.Int("payload_bytes", len(data)))
		r.Body = io.NopCloser(bytes.NewReader(data))

		switch pdu.MessageType {
		case mmspdu.MsgTypeSendReq:
			s.handler.Handle(w, r)
		case mmspdu.MsgTypeForwardReq:
			s.handler.Handle(w, r)
		case mmspdu.MsgTypeNotifyRespInd:
			s.mt.HandleNotifyResp(w, r, pdu)
		case mmspdu.MsgTypeAcknowledgeInd:
			s.mt.HandleAcknowledge(w, r, pdu)
		case mmspdu.MsgTypeDeliveryInd:
			s.mt.HandleDeliveryReport(w, r, pdu)
		case mmspdu.MsgTypeReadRecInd, mmspdu.MsgTypeReadOrigInd:
			s.mt.HandleReadReport(w, r, pdu)
		default:
			log.Debug("mm1 unsupported pdu type", zap.Uint8("message_type", pdu.MessageType))
			http.Error(w, "unsupported message type", http.StatusBadRequest)
		}
		return
	}

	log.Debug("mm1 request rejected", zap.String("content_type", r.Header.Get("Content-Type")))
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
