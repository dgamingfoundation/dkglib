package utils

import (
	"fmt"
	"os"

	"github.com/dgamingfoundation/dkglib/lib/client/context"

	"github.com/dgamingfoundation/dkglib/lib/client"

	"github.com/cosmos/cosmos-sdk/codec"

	"github.com/tendermint/go-amino"
	"github.com/tendermint/tendermint/libs/common"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/auth"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
)

// GasEstimateResponse defines a response definition for tx gas estimation.
type GasEstimateResponse struct {
	GasEstimate uint64 `json:"gas_estimate"`
}

func (gr GasEstimateResponse) String() string {
	return fmt.Sprintf("gas estimate: %d", gr.GasEstimate)
}

// GenerateOrBroadcastMsgs respects CLI flags and outputs a message
func GenerateOrBroadcastMsgs(cliCtx context.CLIContext, txBldr authtypes.TxBuilder, msgs []sdk.Msg, offline bool) error {
	if cliCtx.GenerateOnly {
		return PrintUnsignedStdTx(txBldr, cliCtx, msgs, offline)
	}
	return CompleteAndBroadcastTxCLI(txBldr, cliCtx, msgs)
}

// CompleteAndBroadcastTxCLI implements a utility function that facilitates
// sending a series of messages in a signed transaction given a TxBuilder and a
// QueryContext. It ensures that the account exists, has a proper number and
// sequence set. In addition, it builds and signs a transaction with the
// supplied messages. Finally, it broadcasts the signed transaction to a node.
func CompleteAndBroadcastTxCLI(txBldr authtypes.TxBuilder, cliCtx context.CLIContext, msgs []sdk.Msg) error {
	txBldr, err := PrepareTxBuilder(txBldr, cliCtx)
	if err != nil {
		return err
	}

	fromName := cliCtx.GetFromName()

	if txBldr.SimulateAndExecute() || cliCtx.Simulate {
		txBldr, err = EnrichWithGas(txBldr, cliCtx, msgs)
		if err != nil {
			return err
		}

		gasEst := GasEstimateResponse{GasEstimate: txBldr.Gas()}
		fmt.Fprintf(os.Stderr, "%s\n", gasEst.String())
	}

	if cliCtx.Simulate {
		return nil
	}

	passphrase := client.DefaultKeyPass

	if cliCtx.Passphrase != "" {
		passphrase = cliCtx.Passphrase
	}

	// build and sign the transaction
	txBytes, err := txBldr.BuildAndSign(fromName, passphrase, msgs)
	if err != nil {
		return err
	}

	// broadcast to a Tendermint node
	res, err := cliCtx.BroadcastTx(txBytes)
	if err != nil {
		return err
	}

	return cliCtx.PrintOutput(res)
}

// EnrichWithGas calculates the gas estimate that would be consumed by the
// transaction and set the transaction's respective value accordingly.
func EnrichWithGas(txBldr authtypes.TxBuilder, cliCtx context.CLIContext, msgs []sdk.Msg) (authtypes.TxBuilder, error) {
	_, adjusted, err := simulateMsgs(txBldr, cliCtx, msgs)
	if err != nil {
		return txBldr, err
	}
	return txBldr.WithGas(adjusted), nil
}

// CalculateGas simulates the execution of a transaction and returns
// both the estimate obtained by the query and the adjusted amount.
func CalculateGas(queryFunc func(string, common.HexBytes) ([]byte, int64, error),
	cdc *amino.Codec, txBytes []byte, adjustment float64) (estimate, adjusted uint64, err error) {

	// run a simulation (via /app/simulate query) to
	// estimate gas and update TxBuilder accordingly
	rawRes, _, err := queryFunc("/app/simulate", txBytes)
	if err != nil {
		return
	}
	estimate, err = parseQueryResponse(cdc, rawRes)
	if err != nil {
		return
	}
	adjusted = adjustGasEstimate(estimate, adjustment)
	return
}

// PrintUnsignedStdTx builds an unsigned StdTx and prints it to os.Stdout.
// Don't perform online validation or lookups if offline is true.
func PrintUnsignedStdTx(
	txBldr authtypes.TxBuilder, cliCtx context.CLIContext, msgs []sdk.Msg, offline bool,
) (err error) {

	var stdTx auth.StdTx

	if offline {
		stdTx, err = buildUnsignedStdTxOffline(txBldr, cliCtx, msgs)
	} else {
		stdTx, err = buildUnsignedStdTx(txBldr, cliCtx, msgs)
	}

	if err != nil {
		return
	}

	json, err := cliCtx.Codec.MarshalJSON(stdTx)
	if err == nil {
		fmt.Fprintf(cliCtx.Output, "%s\n", json)
	}

	return
}

// GetTxEncoder return tx encoder from global sdk configuration if ones is defined.
// Otherwise returns encoder with default logic.
func GetTxEncoder(cdc *codec.Codec) (encoder sdk.TxEncoder) {
	encoder = sdk.GetConfig().GetTxEncoder()
	if encoder == nil {
		encoder = auth.DefaultTxEncoder(cdc)
	}
	return
}

// nolint
// SimulateMsgs simulates the transaction and returns the gas estimate and the adjusted value.
func simulateMsgs(txBldr authtypes.TxBuilder, cliCtx context.CLIContext, msgs []sdk.Msg) (estimated, adjusted uint64, err error) {
	txBytes, err := txBldr.BuildTxForSim(msgs)
	if err != nil {
		return
	}
	estimated, adjusted, err = CalculateGas(cliCtx.Query, cliCtx.Codec, txBytes, txBldr.GasAdjustment())
	return
}

func adjustGasEstimate(estimate uint64, adjustment float64) uint64 {
	return uint64(adjustment * float64(estimate))
}

func parseQueryResponse(cdc *amino.Codec, rawRes []byte) (uint64, error) {
	var simulationResult sdk.Result
	if err := cdc.UnmarshalBinaryLengthPrefixed(rawRes, &simulationResult); err != nil {
		return 0, err
	}
	return simulationResult.GasUsed, nil
}

// PrepareTxBuilder populates a TxBuilder in preparation for the build of a Tx.
func PrepareTxBuilder(txBldr authtypes.TxBuilder, cliCtx context.CLIContext) (authtypes.TxBuilder, error) {
	from := cliCtx.GetFromAddress()

	accGetter := authtypes.NewAccountRetriever(cliCtx)
	if err := accGetter.EnsureExists(from); err != nil {
		return txBldr, err
	}

	txbldrAccNum, txbldrAccSeq := txBldr.AccountNumber(), txBldr.Sequence()
	// TODO: (ref #1903) Allow for user supplied account number without
	// automatically doing a manual lookup.
	if txbldrAccNum == 0 || txbldrAccSeq == 0 {
		num, seq, err := authtypes.NewAccountRetriever(cliCtx).GetAccountNumberSequence(from)
		if err != nil {
			return txBldr, err
		}

		if txbldrAccNum == 0 {
			txBldr = txBldr.WithAccountNumber(num)
		}
		if txbldrAccSeq == 0 {
			txBldr = txBldr.WithSequence(seq)
		}
	}

	return txBldr, nil
}

// buildUnsignedStdTx builds a StdTx as per the parameters passed in the
// contexts. Gas is automatically estimated if gas wanted is set to 0.
func buildUnsignedStdTx(txBldr authtypes.TxBuilder, cliCtx context.CLIContext, msgs []sdk.Msg) (stdTx auth.StdTx, err error) {
	txBldr, err = PrepareTxBuilder(txBldr, cliCtx)
	if err != nil {
		return
	}
	return buildUnsignedStdTxOffline(txBldr, cliCtx, msgs)
}

func buildUnsignedStdTxOffline(txBldr authtypes.TxBuilder, cliCtx context.CLIContext, msgs []sdk.Msg) (stdTx auth.StdTx, err error) {
	if txBldr.SimulateAndExecute() {
		txBldr, err = EnrichWithGas(txBldr, cliCtx, msgs)
		if err != nil {
			return
		}

		fmt.Fprintf(os.Stderr, "estimated gas = %v\n", txBldr.Gas())
	}

	stdSignMsg, err := txBldr.BuildSignMsg(msgs)
	if err != nil {
		return
	}

	return auth.NewStdTx(stdSignMsg.Msgs, stdSignMsg.Fee, nil, stdSignMsg.Memo), nil
}