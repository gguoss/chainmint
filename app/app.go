package app

import (
	"encoding/json"
	"context"

	"github.com/chainmint/protocol/state"
	"github.com/chainmint/errors"
	"github.com/chainmint/env"
	"github.com/chainmint/core"
	"github.com/chainmint/protocol/bc/legacy"
	"github.com/chainmint/log"
	"github.com/chainmint/core/rpc"
	abciTypes "github.com/tendermint/abci/types"

	cmtTypes "github.com/chainmint/types"
)

var (
	coreURL = env.String("CORE_URL", "http://localhost:1999")
)
// ChainmintApplication implements an ABCI application
type ChainmintApplication struct {

	// backend handles the chain state machine
	// and wrangles other services started by an chain node (eg. tx pool)
	backend *core.API // backend chain struct

	// a closure to return the latest current state from the chain
	currentState func() (*legacy.Block, *state.Snapshot)

	// strategy for validator compensation
	strategy *cmtTypes.Strategy
	BlockTime uint64
}

// NewChainmintApplication creates the abci application for Chainmint
func NewChainmintApplication(strategy *cmtTypes.Strategy) *ChainmintApplication {
	app := &ChainmintApplication{
		strategy:     strategy,
	}
	return app
}

func (app *ChainmintApplication) Init(backend *core.API/*, client *rpc.Client*/) {
	app.backend = backend
	app.currentState = backend.Chain().State
}

// Info returns information about the last height and app_hash to the tendermint engine
func (app *ChainmintApplication) Info() abciTypes.ResponseInfo {
	log.Printf(context.Background(), "Info")
	currentBlock, _ := app.currentState()
	if currentBlock == nil {
		return abciTypes.ResponseInfo{
			Data:   "ABCIChain",
			LastBlockHeight: uint64(0),
			LastBlockAppHash: []byte{},
		}
	}
	height := currentBlock.BlockHeight()
	hash := currentBlock.Hash().Bytes()

	// This check determines whether it is the first time chainmint gets started.
	// If it is the first time, then we have to respond with an empty hash, since
	// that is what tendermint expects.
	if height == 0 {
		return abciTypes.ResponseInfo{
			Data:             "ABCIChain",
			LastBlockHeight:  uint64(0),
			LastBlockAppHash: []byte{},
		}
	}

	return abciTypes.ResponseInfo{
		Data:             "ABCIChain",
		LastBlockHeight:  height,
		LastBlockAppHash: hash,
	}
}

// SetOption sets a configuration option
func (app *ChainmintApplication) SetOption(key string, value string) (log string) {
	//log.Info("SetOption")
	return ""
}

// InitChain initializes the validator set
func (app *ChainmintApplication) InitChain(validators []*abciTypes.Validator) {
	log.Printf(context.Background(), "InitChain")
	//app.setvalidators(validators)
	app.SetValidators(validators)
}

// CheckTx checks a transaction is valid but does not mutate the state
func (app *ChainmintApplication) CheckTx(txBytes []byte) abciTypes.Result {
	tx, err := decodeTx(txBytes)
	log.Printf(context.Background(), "Received CheckTx", "tx", tx)
	if err != nil {
		return abciTypes.ErrEncodingError.AppendLog(err.Error())
	}

	return app.validateTx(tx)
}

// DeliverTx executes a transaction against the latest state
func (app *ChainmintApplication) DeliverTx(txBytes []byte) abciTypes.Result {
	tx, err := decodeTx(txBytes)
	if err != nil {
		return abciTypes.ErrEncodingError.AppendLog(err.Error())
	}

	log.Printf(context.Background(), "Got DeliverTx", "tx", tx)
	app.backend.Generator().Submit(context.Background(), tx)
	app.CollectTx(tx)

	return abciTypes.OK
}

// BeginBlock starts a new chain block
func (app *ChainmintApplication) BeginBlock(hash []byte, tmHeader *abciTypes.Header) {
	log.Printf(context.Background(), "BeginBlock")
	app.BlockTime = tmHeader.Time
}

// EndBlock accumulates rewards for the validators and updates them
func (app *ChainmintApplication) EndBlock(height uint64) abciTypes.ResponseEndBlock {
	log.Printf(context.Background(), "EndBlock")
	return app.GetUpdatedValidators()
}

// Commit commits the block and returns a hash of the current state
func (app *ChainmintApplication) Commit() abciTypes.Result {
	log.Printf(context.Background(), "Commit")
	err, blockHash := app.backend.Generator().MakeBlock(context.Background(), app.BlockTime)
	if err != nil {
		log.Error(context.Background(), err)
	}
	return abciTypes.NewResultOK(blockHash[:], "")
}

// Query queries the state of ChainmintApplication
func (app *ChainmintApplication) Query(query abciTypes.RequestQuery) abciTypes.ResponseQuery {
	log.Printf(context.Background(), "Query")
	client := &rpc.Client{
						BaseURL: *coreURL,
						Client: app.backend.HttpClient(),
						}
	var in jsonRequest
	if err := json.Unmarshal(query.Data, &in); err != nil {
		return abciTypes.ResponseQuery{Code: abciTypes.ErrEncodingError.Code, Log: err.Error()}
	}
	var result map[string]interface{}
	if err := client.Call(context.Background(), query.Path, in, &result); err != nil {
		return abciTypes.ResponseQuery{Code: abciTypes.ErrInternalError.Code, Log: err.Error()}
	}

	bytes, err := json.Marshal(result)
	if err != nil {
		return abciTypes.ResponseQuery{Code: abciTypes.ErrInternalError.Code, Log: err.Error()}
	}
	return abciTypes.ResponseQuery{Code: abciTypes.OK.Code, Value: bytes}
}

//-------------------------------------------------------

// validateTx checks the validity of a tx against the blockchain's current state.
// it duplicates the logic in chain's tx_pool
func (app *ChainmintApplication) validateTx(tx *legacy.Tx) abciTypes.Result {
	err := app.backend.Chain().ValidateTx(tx.Tx)
	if err != nil {
		return abciTypes.ErrUnknownRequest.AppendLog(errors.Detail(err))
	}
	return abciTypes.OK
}
