// Copyright (c) 2024 Project Illium
// Use of this source code is governed by an MIT
// license that can be found in the LICENSE file.

package repo

import (
	"github.com/ipfs/go-datastore"
)

const (
	// NetworkKeyDatastoreKey is the datastore key for the network (libp2p) private key.
	NetworkKeyDatastoreKey = "/ilxd/libp2pkey/"
	// ValidatorDatastoreKeyPrefix is the datastore key prefix for the validators.
	ValidatorDatastoreKeyPrefix = "/ilxd/validator/"
	// ValidatorSetLastFlushHeight is the datastore key for last flush height of the validator set.
	ValidatorSetLastFlushHeight = "/ilxd/validatorsetlastflushheight/"
	// ValidatorSetConsistencyStatusKey is the datastore key for the validator set flush state.
	ValidatorSetConsistencyStatusKey = "/ilxd/validatorsetconsistencystatus/"
	// AccumulatorConsistencyStatusKey is the datastore key for the accumulator flush state.
	AccumulatorConsistencyStatusKey = "/ilxd/accumulatorconsistencystatus/"
	// AccumulatorLastFlushHeight is the datastore key for last flush height of the accumulator.
	AccumulatorLastFlushHeight = "/ilxd/accumulatorlastflushheight/"
	// BlockByHeightKeyPrefix is the datastore key prefix for mapping block heights to block IDs.
	BlockByHeightKeyPrefix = "/ilxd/blockbyheight/"
	// BlockKeyPrefix is the datastore key prefix for storing block headers by blockID.
	BlockKeyPrefix = "/ilxd/block/"
	// BlockTxsKeyPrefix is the datastore key prefix mapping a block ID to a list of txids.
	BlockTxsKeyPrefix = "/ilxd/blocktxs/"
	// BlockIndexStateKey is the datastore key used to store the block index best state.
	BlockIndexStateKey = "/ilxd/blockindex/"
	// NullifierKeyPrefix is the datastore key prefix for storing nullifiers in the nullifier set.
	NullifierKeyPrefix = "/ilxd/nullifier/"
	// TxoRootKeyPrefix is the datastore key prefix for storing a txo root in the database.
	TxoRootKeyPrefix = "/ilxd/txoroot/"
	// TreasuryBalanceKey is the datastire key for storing the balance of the treasury in the database.
	TreasuryBalanceKey = "/ilxd/treasury/"
	// AccumulatorStateKey is the datastore key for storing the accumulator state.
	AccumulatorStateKey = "/ilxd/accumulator/"
	// AccumulatorCheckpointKey is the datastore key for storing accumulator checkpoints.
	AccumulatorCheckpointKey = "/ilxd/accumulatorcheckpoint/"
	// CoinSupplyKey is the datastore key for storing the current supply of coins.
	CoinSupplyKey = "/ilxd/coinsupply/"
	// IndexerHeightKeyPrefix is the datastore key prefix for mapping indexers to sync heights.
	IndexerHeightKeyPrefix = "/ilxd/indexerheight/"
	// IndexKeyPrefix is the datastore key used by each indexer. This must be extended to use.
	IndexKeyPrefix = "/ilxd/index/"
	// ConnGaterKeyPrefix is the datastore namespace key used by the conngater.
	ConnGaterKeyPrefix = "/ilxd/conngater/"
	// AutostakeDatastoreKey is the datastore key used to store the autostake bool.
	AutostakeDatastoreKey = "/ilxd/autostake/"
	// PrunedBlockchainDatastoreKey is the datastore key used to store a flag setting whether the chain has ever been pruned.
	PrunedBlockchainDatastoreKey = "/ilxd/pruned/"
	// CachedAddrInfoDatastoreKey is the datastore key used to persist addrinfos from the peerstore.
	CachedAddrInfoDatastoreKey = "/ilxd/peerstore/addrinfo/"
)

type Datastore interface {
	datastore.Datastore
	datastore.Batching
	datastore.PersistentDatastore
	datastore.TxnDatastore
}
