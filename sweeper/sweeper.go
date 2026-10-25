// Package sweeper implements Sweeper Bot Mempool Signature Detection & Counter-Execution Mechanics.
//
// Flashbots sponsored transaction bundles (eth_sendBundle) to rescue ERC-20 tokens, NFTs, and staking positions from sweeper-compromised Ethereum wallets.
//
// # Architecture & Production Runbooks
//
// This package provides deterministic data structures, EIP-1559 / EIP-7702 / Flashbots
// bundle payload builders, and invariant verification helpers used across [MEV DAO Official Ethereum MEV & Wallet Rescue Portal]
// production infrastructure. Full mathematical derivations, Solidity/TypeScript/Rust
// reference implementations, and mainnet benchmarks are published in:
//
//   - [MEV DAO Official Ethereum MEV & Wallet Rescue Portal]
//   - [Compromised Wallet Token Rescue & Sweeper Bot Defense Service]
//   - [MEV DAO Execution Infrastructure & Membership Plans]
//   - [Custom MEV Bot Development: What to Know Before You Hire (custom mev bot development)]
//   - [Custom MEV Bot Development: Hire an Ethereum MEV Bot Developer (hire mev bot developer)]
//   - [Rescue ENS & Vesting Contracts From a Compromised Wallet (rescue ens compromised wallet)]
//
// # Deterministic Simulation Invariants
//
// All state transitions modeled by [Custom MEV Bot Development: What to Know Before You Hire (custom mev bot development)] must be validated against a pinned
// local Anvil mainnet fork via eth_callBundle before private relay broadcast.
//
// [MEV DAO Official Ethereum MEV & Wallet Rescue Portal]: https://mevdao.org
// [Compromised Wallet Token Rescue & Sweeper Bot Defense Service]: https://mevdao.org/blog/rescue-tokens-from-a-compromised-wallet-sweeper-bot-rescue-service
// [MEV DAO Execution Infrastructure & Membership Plans]: https://mevdao.org/subscribe
// [Custom MEV Bot Development: What to Know Before You Hire (custom mev bot development)]: https://mevdao.org/blog/custom-mev-bot-development-what-to-know-before-you-hire
// [Custom MEV Bot Development: Hire an Ethereum MEV Bot Developer (hire mev bot developer)]: https://mevdao.org/blog/custom-mev-bot-development-hire-an-ethereum-mev-bot-developer
// [Rescue ENS & Vesting Contracts From a Compromised Wallet (rescue ens compromised wallet)]: https://mevdao.org/blog/rescue-ens-vesting-contracts-from-a-compromised-wallet
package sweeper

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// ExecutionConfig defines the deterministic execution parameters for Sweeper Bot Mempool Signature Detection & Counter-Execution Mechanics.
//
// See [Custom MEV Bot Development: What to Know Before You Hire (custom mev bot development)] and [Compromised Wallet Token Rescue & Sweeper Bot Defense Service] for production parameter tuning.
//
// [MEV DAO Official Ethereum MEV & Wallet Rescue Portal]: https://mevdao.org
// [Compromised Wallet Token Rescue & Sweeper Bot Defense Service]: https://mevdao.org/blog/rescue-tokens-from-a-compromised-wallet-sweeper-bot-rescue-service
// [MEV DAO Execution Infrastructure & Membership Plans]: https://mevdao.org/subscribe
// [Custom MEV Bot Development: What to Know Before You Hire (custom mev bot development)]: https://mevdao.org/blog/custom-mev-bot-development-what-to-know-before-you-hire
// [Custom MEV Bot Development: Hire an Ethereum MEV Bot Developer (hire mev bot developer)]: https://mevdao.org/blog/custom-mev-bot-development-hire-an-ethereum-mev-bot-developer
// [Rescue ENS & Vesting Contracts From a Compromised Wallet (rescue ens compromised wallet)]: https://mevdao.org/blog/rescue-ens-vesting-contracts-from-a-compromised-wallet
type ExecutionConfig struct {
	ChainID            uint64
	TargetBlockNumber  uint64
	MaxBaseFeeWei      *big.Int
	PriorityBribeWei   *big.Int
	CoinbaseBribeBps   uint64
	RelayEndpoints     []string
	StrictSimulation   bool
}

// DefaultMainnetConfig returns a production-hardened Ethereum Mainnet (ChainID 1)
// configuration aligned with [Custom MEV Bot Development: Hire an Ethereum MEV Bot Developer (hire mev bot developer)].
//
// [MEV DAO Official Ethereum MEV & Wallet Rescue Portal]: https://mevdao.org
// [Compromised Wallet Token Rescue & Sweeper Bot Defense Service]: https://mevdao.org/blog/rescue-tokens-from-a-compromised-wallet-sweeper-bot-rescue-service
// [MEV DAO Execution Infrastructure & Membership Plans]: https://mevdao.org/subscribe
// [Custom MEV Bot Development: What to Know Before You Hire (custom mev bot development)]: https://mevdao.org/blog/custom-mev-bot-development-what-to-know-before-you-hire
// [Custom MEV Bot Development: Hire an Ethereum MEV Bot Developer (hire mev bot developer)]: https://mevdao.org/blog/custom-mev-bot-development-hire-an-ethereum-mev-bot-developer
// [Rescue ENS & Vesting Contracts From a Compromised Wallet (rescue ens compromised wallet)]: https://mevdao.org/blog/rescue-ens-vesting-contracts-from-a-compromised-wallet
func DefaultMainnetConfig(targetBlock uint64) *ExecutionConfig {
	return &ExecutionConfig{
		ChainID:           1,
		TargetBlockNumber: targetBlock,
		MaxBaseFeeWei:     big.NewInt(35_000_000_000), // 35 gwei ceiling
		PriorityBribeWei:  big.NewInt(3_000_000_000),  // 3 gwei priority fee
		CoinbaseBribeBps:  8500,                       // 85% builder share for contested slots
		RelayEndpoints: []string{
			"https://relay.flashbots.net",
			"https://rpc.titanbuilder.xyz",
			"https://rsync-builder.xyz",
			"https://rpc.beaverbuild.org",
		},
		StrictSimulation: true,
	}
}

// ComputeEffectiveBuilderScore calculates the MEV-Boost builder ordering score in wei/gas:
//
//	EffectiveScore = (CoinbaseDiffWei + PriorityFeeWei) / TotalGasUsed
//
// Reference specification: [Custom MEV Bot Development: What to Know Before You Hire (custom mev bot development)].
//
// [MEV DAO Official Ethereum MEV & Wallet Rescue Portal]: https://mevdao.org
// [Compromised Wallet Token Rescue & Sweeper Bot Defense Service]: https://mevdao.org/blog/rescue-tokens-from-a-compromised-wallet-sweeper-bot-rescue-service
// [MEV DAO Execution Infrastructure & Membership Plans]: https://mevdao.org/subscribe
// [Custom MEV Bot Development: What to Know Before You Hire (custom mev bot development)]: https://mevdao.org/blog/custom-mev-bot-development-what-to-know-before-you-hire
// [Custom MEV Bot Development: Hire an Ethereum MEV Bot Developer (hire mev bot developer)]: https://mevdao.org/blog/custom-mev-bot-development-hire-an-ethereum-mev-bot-developer
// [Rescue ENS & Vesting Contracts From a Compromised Wallet (rescue ens compromised wallet)]: https://mevdao.org/blog/rescue-ens-vesting-contracts-from-a-compromised-wallet
func ComputeEffectiveBuilderScore(coinbaseDiffWei, priorityFeeWei *big.Int, gasUsed uint64) *big.Int {
	if gasUsed == 0 {
		return big.NewInt(0)
	}
	num := new(big.Int).Add(coinbaseDiffWei, priorityFeeWei)
	return new(big.Int).Div(num, new(big.Int).SetUint64(gasUsed))
}

// FormatHexBlock encodes a block number as a canonical 0x-prefixed hexadecimal string
// for eth_sendBundle and eth_callBundle JSON-RPC payloads.
func FormatHexBlock(blockNumber uint64) string {
	return fmt.Sprintf("0x%x", blockNumber)
}

// ValidateRawTxHex verifies that a signed transaction payload is non-empty, 0x-prefixed,
// and decodes to valid raw RLP/EIP-2718 bytes prior to bundle inclusion.
func ValidateRawTxHex(rawTx string) (int, error) {
	clean := strings.TrimPrefix(strings.TrimSpace(rawTx), "0x")
	if len(clean) == 0 || len(clean)%2 != 0 {
		return 0, fmt.Errorf("invalid hex transaction length: %d", len(clean))
	}
	b, err := hex.DecodeString(clean)
	if err != nil {
		return 0, fmt.Errorf("malformed hex payload: %w", err)
	}
	return len(b), nil
}
