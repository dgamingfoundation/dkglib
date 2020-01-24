package onChain

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"os"

	authtxb "github.com/corestario/cosmos-utils/client/authtypes"
	"github.com/corestario/cosmos-utils/client/context"
	"github.com/corestario/cosmos-utils/client/utils"
	"github.com/corestario/dkglib/lib/alias"
	"github.com/corestario/dkglib/lib/dealer"
	"github.com/corestario/dkglib/lib/msgs"
	"github.com/corestario/dkglib/lib/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	tmtypes "github.com/tendermint/tendermint/alias"
	"github.com/tendermint/tendermint/libs/events"
	"github.com/tendermint/tendermint/libs/log"
)

type OnChainDKG struct {
	cli       *context.Context
	txBldr    *authtxb.TxBuilder
	dealer    dealer.Dealer
	typesList []alias.DKGDataType
	logger    log.Logger
}

func NewOnChainDKG(cli *context.Context, txBldr *authtxb.TxBuilder) *OnChainDKG {
	return &OnChainDKG{
		cli:    cli,
		txBldr: txBldr,
		logger: log.NewTMLogger(os.Stdout),
	}
}

func (m *OnChainDKG) GetVerifier() (types.Verifier, error) {
	return m.dealer.GetVerifier()
}

func (m *OnChainDKG) ProcessBlock() (error, bool) {
	for _, dataType := range []alias.DKGDataType{
		alias.DKGPubKey,
		alias.DKGDeal,
		alias.DKGResponse,
		alias.DKGJustification,
		alias.DKGCommits,
		alias.DKGComplaint,
		alias.DKGReconstructCommit,
	} {
		messages, err := m.getDKGMessages(dataType)
		if err != nil {
			return fmt.Errorf("failed to getDKGMessages: %v", err), false
		}
		var handler func(msg *alias.DKGData) error
		switch dataType {
		case alias.DKGPubKey:
			handler = m.dealer.HandleDKGPubKey
		case alias.DKGDeal:
			handler = m.dealer.HandleDKGDeal
		case alias.DKGResponse:
			handler = m.dealer.HandleDKGResponse
		case alias.DKGJustification:
			handler = m.dealer.HandleDKGJustification
		case alias.DKGCommits:
			handler = m.dealer.HandleDKGCommit
		case alias.DKGComplaint:
			handler = m.dealer.HandleDKGComplaint
		case alias.DKGReconstructCommit:
			handler = m.dealer.HandleDKGReconstructCommit
		}
		for _, msg := range messages {
			if err := handler(msg.Data); err != nil {
				return fmt.Errorf("failed to handle message: %v", err), false
			}
		}
	}

	if _, err := m.dealer.GetVerifier(); err == types.ErrDKGVerifierNotReady {
		m.logger.Info("Verifier Not ready")
		return nil, false
	} else if err != nil {
		return fmt.Errorf("DKG round failed: %v", err), false
	}

	return nil, true
}

func (m *OnChainDKG) StartRound(
	validators *tmtypes.ValidatorSet,
	pv tmtypes.PrivValidator,
	eventFirer events.Fireable,
	logger log.Logger,
	startRound int) error {
	m.dealer = dealer.NewDKGDealer(validators, pv, m.sendMsg, eventFirer, logger, startRound)
	if err := m.dealer.Start(); err != nil {
		return fmt.Errorf("failed to start dealer: %v", err)
	}

	return nil
}

func (m *OnChainDKG) GetLosers() []*tmtypes.Validator {
	return m.dealer.GetLosers()
}

func (m *OnChainDKG) sendMsg(data *alias.DKGData) error {
	msg := msgs.NewMsgSendDKGData(data, m.cli.GetFromAddress())
	if err := msg.ValidateBasic(); err != nil {
		return fmt.Errorf("failed to validate basic: %v", err)
	}

	err := utils.GenerateOrBroadcastMsgs(*m.cli, *m.txBldr, []sdk.Msg{msg}, false)
	tempTxBldr := m.txBldr.WithSequence(m.txBldr.Sequence() + 1)
	m.txBldr = &tempTxBldr
	if err != nil {
		return fmt.Errorf("failed to broadcast msg: %v", err)
	}

	return nil
}

func (m *OnChainDKG) getDKGMessages(dataType alias.DKGDataType) ([]*msgs.MsgSendDKGData, error) {
	res, _, err := m.cli.QueryWithData(fmt.Sprintf("custom/randapp/dkgData/%d", dataType), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to query for DKG data: %v", err)
	}

	var data []*msgs.MsgSendDKGData
	var dec = gob.NewDecoder(bytes.NewBuffer(res))
	if err := dec.Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to decode DKG data: %v", err)
	}

	if dataType == 0 {
		m.logger.Info("DKG DATA", "data", data, "dataLen", len(data))
	}

	return data, nil
}

func (m *OnChainDKG) StartDKGRound(validators *tmtypes.ValidatorSet) error {
	return nil
}
