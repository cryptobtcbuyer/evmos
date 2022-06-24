package keeper

import (
	"github.com/armon/go-metrics"
	"github.com/cosmos/cosmos-sdk/telemetry"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"

	evmtypes "github.com/tharsis/ethermint/x/evm/types"

	"github.com/evmos/evmos/v5/x/fees/types"
)

var _ evmtypes.EvmHooks = Hooks{}

// Hooks wrapper struct for fees keeper
type Hooks struct {
	k Keeper
}

// Hooks return the wrapper hooks struct for the Keeper
func (k Keeper) Hooks() Hooks {
	return Hooks{k}
}

// PostTxProcessing implements EvmHooks.PostTxProcessing. After each successful
// interaction with a registered contract, the contract deployer (or if set the
// withdrawer) receives a share from the transaction fees paid by the user.
func (k Keeper) PostTxProcessing(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt) error {
	// check if the fees are globally enabled
	params := k.GetParams(ctx)
	if !params.EnableFees {
		return nil
	}

	contract := msg.To()
	if contract == nil {
		return nil
	}

	// if the contract is not registered to receive fees, do nothing
	fee, found := k.GetFee(ctx, *contract)
	if !found {
		return nil
	}

	withdrawAddr := fee.WithdrawAddress
	if withdrawAddr == "" {
		withdrawAddr = fee.DeployerAddress
	}

	txFee := sdk.NewIntFromUint64(receipt.GasUsed).Mul(sdk.NewIntFromBigInt(msg.GasPrice()))
	developerFee := txFee.ToDec().Mul(params.DeveloperShares).TruncateInt()
	evmDenom := k.evmKeeper.GetParams(ctx).EvmDenom
	fees := sdk.Coins{{Denom: evmDenom, Amount: developerFee}}

	// distribute the fees to the contract deployer / withdraw address
	err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, k.feeCollectorName, sdk.MustAccAddressFromBech32(withdrawAddr), fees)
	if err != nil {
		return sdkerrors.Wrapf(
			err,
			"fee collector account failed to distribute developer fees (%s) to withdraw address %s. contract %s",
			fees, withdrawAddr, contract,
		)
	}

	defer func() {
		if developerFee.IsInt64() {
			telemetry.IncrCounterWithLabels(
				[]string{types.ModuleName, "distribute", "total"},
				float32(developerFee.Int64()),
				[]metrics.Label{
					telemetry.NewLabel("sender", msg.From().String()),
					telemetry.NewLabel("withdrawer", withdrawAddr),
					telemetry.NewLabel("contract", fee.ContractAddress),
				},
			)
		}
	}()

	ctx.EventManager().EmitEvents(
		sdk.Events{
			sdk.NewEvent(
				types.EventTypeDistributeDevFee,
				sdk.NewAttribute(sdk.AttributeKeySender, msg.From().String()),
				sdk.NewAttribute(types.AttributeKeyContract, contract.String()),
				sdk.NewAttribute(types.AttributeKeyWithdrawAddress, withdrawAddr),
			),
		},
	)

	return nil
}

// evm hook
func (h Hooks) PostTxProcessing(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt) error {
	return h.k.PostTxProcessing(ctx, msg, receipt)
}
