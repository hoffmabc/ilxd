// Copyright (c) 2024 The illium developers
// Use of this source code is governed by an MIT
// license that can be found in the LICENSE file.

package sync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	inet "github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/project-illium/ilxd/blockchain"
	"github.com/project-illium/ilxd/net"
	"github.com/project-illium/ilxd/params"
	"github.com/project-illium/ilxd/types"
	"github.com/project-illium/ilxd/types/blocks"
	"github.com/project-illium/ilxd/types/transactions"
	"github.com/project-illium/ilxd/zk"
	"math"
	"math/rand"
	"sync"
	"time"
)

const (
	nextHeightQuerySize = 8
	bestHeightQuerySize = 100
	lookaheadSize       = 10000
	evaluationWindow    = 5000
)

// SyncManager is responsible for trustlessly syncing the blockchain
// to the tip of the chain.
type SyncManager struct {
	ctx             context.Context
	params          *params.NetworkParams
	network         *net.Network
	chainService    *ChainService
	chain           *blockchain.Blockchain
	consensuChooser ConsensusChooser
	buckets         map[types.ID][]peer.ID
	bucketMtx       sync.RWMutex
	currentMtx      sync.RWMutex
	current         bool
	syncMtx         sync.Mutex
	behavorFlag     blockchain.BehaviorFlags
	proofCache      *blockchain.ProofCache
	sigCache        *blockchain.SigCache
	verifier        zk.Verifier
	callback        func()
	quit            chan struct{}
}

// ConsensusChooser is an interface function which polls the consensus engine
// to determine the best block at the height.
type ConsensusChooser func([]*blocks.Block) (types.ID, error)

// SyncManagerConfig holds the configuration options for the SyncManager
type SyncManagerConfig struct {
	Ctx               context.Context
	Chain             *blockchain.Blockchain
	Network           *net.Network
	Params            *params.NetworkParams
	CS                *ChainService
	Chooser           ConsensusChooser
	ProofCache        *blockchain.ProofCache
	SigCache          *blockchain.SigCache
	Verifier          zk.Verifier
	IsCurrentCallback func()
}

// NewSyncManager returns a new initialized SyncManager
func NewSyncManager(cfg *SyncManagerConfig) *SyncManager {
	sm := &SyncManager{
		ctx:             cfg.Ctx,
		params:          cfg.Params,
		network:         cfg.Network,
		chainService:    cfg.CS,
		chain:           cfg.Chain,
		consensuChooser: cfg.Chooser,
		proofCache:      cfg.ProofCache,
		sigCache:        cfg.SigCache,
		verifier:        cfg.Verifier,
		buckets:         make(map[types.ID][]peer.ID),
		syncMtx:         sync.Mutex{},
		bucketMtx:       sync.RWMutex{},
		currentMtx:      sync.RWMutex{},
		callback:        cfg.IsCurrentCallback,
		behavorFlag:     blockchain.BFNone,
		quit:            make(chan struct{}),
	}
	notifier := &inet.NotifyBundle{
		DisconnectedF: sm.bucketPeerDisconnected,
	}

	sm.network.Host().Network().Notify(notifier)

	return sm
}

// Start begins the process of syncing to the tip of the chain
func (sm *SyncManager) Start() {
	sm.syncMtx.Lock()
	defer sm.syncMtx.Unlock()

	sm.quit = make(chan struct{})

	_, startheight, _ := sm.chain.BestBlock()

	// Sync up to the checkpoints if we're not already past them.
	if len(sm.params.Checkpoints) > 0 && startheight < sm.params.Checkpoints[len(sm.params.Checkpoints)-1].Height {
		log.WithCaller(true).Trace("Syncing to checkpoints", log.Args("start height", startheight))
		sm.syncToCheckpoints(startheight)
	}

	// Before we start we want to do a large peer query to see if there
	// are any forks out there. If there are, we will sort the peers into
	// buckets depending on which fork they are on.
	log.WithCaller(true).Trace("Waiting for enough peers to start sync")
	sm.waitForPeers()
	for {
		err := sm.populatePeerBuckets()
		if err != nil {
			select {
			case <-sm.quit:
				return
			default:
				continue
			}
		}
		break
	}
	log.WithCaller(true).Trace("Starting sync", log.ArgsFromMap(map[string]any{
		"peers":   len(sm.network.Host().Network().Peers()),
		"buckets": len(sm.buckets),
	}))

	// Now we can continue to sync the rest of the chain.
syncLoop:
	for {
		select {
		case <-sm.quit:
			return
		default:
		}
		sm.currentMtx.RLock()
		if sm.current {
			sm.currentMtx.RUnlock()
			return
		}
		sm.currentMtx.RUnlock()
		// We'll start by querying a subset of our peers ask them for what
		// block they have at height + lookaheadSize.
		//
		// We will make sure at least one peer from each bucket is part of
		// the subset that we query. This will ensure that, if there is a
		// fork, we will encounter it as we sync forward.
		bestID, height, _ := sm.chain.BestBlock()
		log.WithCaller(true).Trace("Syncing blocks", log.ArgsFromMap(map[string]any{
			"current height": height,
			"to height":      height + lookaheadSize,
			"current tip":    bestID.String(),
		}))

		blockMap, err := sm.queryPeersForBlockID(height + lookaheadSize)
		if err != nil {
			time.Sleep(time.Second * 10)
			continue
		}
		if len(blockMap) == 0 {
			sm.SetCurrent()
			return
		} else if len(blockMap) == 1 {
			// All peers agree on the blockID at the requested height. This is good.
			// We'll just sync up to this height.
			for blockID, p := range blockMap {
				log.WithCaller(true).Trace("All query peers in agreement", log.ArgsFromMap(map[string]any{
					"height":  height + lookaheadSize,
					"blockID": blockID.String(),
				}))

				err := sm.syncBlocks(p, height+1, height+lookaheadSize, bestID, blockID, sm.behavorFlag)
				if err != nil {
					log.Debug("Error syncing blocks", log.ArgsFromMap(map[string]any{
						"peer":           p,
						"current height": height,
						"sync to height": height + lookaheadSize,
						"error":          err,
					}))
				}
				break
			}
		} else {
			// The peers disagree on the block at the requested height. This sucks.
			// We'll download the evaluation window for each chain and select the one
			// with the best chain score.
			//
			// Step one is we need to find the fork point.
			forkBlock, forkHeight, err := sm.findForkPoint(height, height+lookaheadSize, blockMap)
			if err != nil {
				log.Debug("Error find fork point", log.Args("error", err))
				continue
			}
			log.WithCaller(true).Trace("Query peers not in agreement", log.ArgsFromMap(map[string]any{
				"forkHeight": forkHeight,
				"forkBlock":  forkBlock.String(),
			}))

			// Step two is sync up to fork point.
			if forkHeight > height {
				for _, p := range blockMap {
					err := sm.syncBlocks(p, height+1, forkHeight, bestID, forkBlock, sm.behavorFlag)
					if err != nil {
						log.Debug("Error syncing to fork point", log.ArgsFromMap(map[string]any{
							"peer":           p,
							"current height": height,
							"fork height":    forkHeight,
							"error":          err,
						}))
						continue syncLoop
					}
					break
				}
			}

			var (
				scores      = make(map[types.ID]blockchain.ChainScore)
				syncTo      = make(map[types.ID]*blocks.Block)
				tipOfChain  = true
				firstBlocks = make([]*blocks.Block, 0, len(blockMap))
				firstMap    = make(map[types.ID]types.ID)
			)

			// Step three is to download the evaluation window for each side of the fork.
			for blockID, p := range blockMap {
				if blockID == forkBlock {
					continue
				}
				blks, err := sm.downloadEvalWindow(p, forkHeight+1)
				if err != nil {
					log.Debug("Sync peer failed to serve evaluation window", log.Args("peer", p))
					sm.network.IncreaseBanscore(p, 101, 0)
					continue syncLoop
				}
				firstBlocks = append(firstBlocks, blks[0])

				// Step four is to compute the chain score for each side of the fork.
				score, err := sm.chain.CalcChainScore(blks, sm.behavorFlag)
				if err != nil {
					log.Debug("Sync peer served invalid evaluation window", log.Args("peer", p))
					sm.network.IncreaseBanscore(p, 101, 0)
					continue syncLoop
				}
				if len(blks) < evaluationWindow {
					score = score / blockchain.ChainScore(len(blks)) * evaluationWindow
				} else {
					tipOfChain = false
				}
				scores[blockID] = score
				syncTo[blockID] = blks[len(blks)-1]
				firstMap[blks[0].ID()] = blockID
			}

			// Next, select the fork with the best chain score.
			var (
				bestScore = blockchain.ChainScore(math.MaxInt32)
				bestID    types.ID
			)
			if tipOfChain {
				log.WithCaller(true).Trace("Fork is near the tip of chain. Using consensus chooser.", log.ArgsFromMap(map[string]any{
					"forkHeight": forkHeight,
					"forkBlock":  forkBlock.String(),
				}))
				bestID, err = sm.consensuChooser(firstBlocks)
				if err != nil {
					log.WithCaller(true).Error("Sync error choosing between tips", log.Args("error", err))
					continue syncLoop
				}
				bestID = firstMap[bestID]
			} else {
				for blockID, score := range scores {
					if score < bestScore {
						bestScore = score
						bestID = blockID
					}
				}
			}
			log.WithCaller(true).Trace("Selected best chain", log.Args("bestID", bestID.String()))
			// And ban the nodes on bad fork
			if len(firstBlocks) > 1 {
				for blockID, p := range blockMap {
					if blockID != bestID {
						sm.network.IncreaseBanscore(p, 101, 0)
						sm.bucketMtx.Lock()
						var banBucket types.ID
					bucketLoop:
						for bucketID, bucket := range sm.buckets {
							for _, pid := range bucket {
								if pid == p {
									banBucket = bucketID
									break bucketLoop
								}
							}
						}
						bucket, ok := sm.buckets[banBucket]
						if ok {
							for _, p2 := range bucket {
								sm.network.IncreaseBanscore(p2, 101, 0)
							}
						}
						delete(sm.buckets, banBucket)
						sm.bucketMtx.Unlock()
					}
				}
			}

			// Finally sync to the best fork.
			currentID, height, _ := sm.chain.BestBlock()
			err = sm.syncBlocks(blockMap[bestID], height+1, syncTo[bestID].Header.Height, currentID, syncTo[bestID].ID(), sm.behavorFlag)
			if err != nil {
				log.Debug("Error syncing to best fork", log.ArgsFromMap(map[string]any{
					"peer":  blockMap[bestID],
					"error": err,
				}))
				continue syncLoop
			}
		}
	}
}

// Close stops the sync and resets the SyncManager.
// It can be restarted after this point.
func (sm *SyncManager) Close() {
	sm.currentMtx.RLock()
	defer sm.currentMtx.RUnlock()

	sm.current = false
	close(sm.quit)
	sm.syncMtx.Lock()
	defer sm.syncMtx.Unlock()
}

// IsCurrent returns whether the SyncManager believes it is synced
// to the tip of the chain.
func (sm *SyncManager) IsCurrent() bool {
	sm.currentMtx.RLock()
	defer sm.currentMtx.RUnlock()

	return sm.current
}

// SetCurrent sets the sync manager to current. This will stop the sync.
func (sm *SyncManager) SetCurrent() {
	sm.currentMtx.Lock()
	defer sm.currentMtx.Unlock()

	if !sm.current {
		log.Info("Blockchain synced to tip")
	}
	sm.current = true
	if sm.callback != nil {
		go sm.callback()
	}

}

func (sm *SyncManager) bucketPeerDisconnected(_ inet.Network, conn inet.Conn) {
	sm.bucketMtx.Lock()
	defer sm.bucketMtx.Unlock()

	for blockID, bucket := range sm.buckets {
		for i := len(bucket) - 1; i >= 0; i-- {
			if bucket[i] == conn.RemotePeer() {
				sm.buckets[blockID] = append(sm.buckets[blockID][:i], sm.buckets[blockID][i+1:]...)
			}
		}
		if len(sm.buckets[blockID]) == 0 {
			delete(sm.buckets, blockID)
		}
	}
}

func (sm *SyncManager) queryPeersForBlockID(height uint32) (map[types.ID]peer.ID, error) {
	peers := sm.syncPeers()
	if len(peers) == 0 {
		return nil, errors.New("no peers to query")
	}
	_, bestHeight, _ := sm.chain.BestBlock()
	size := nextHeightQuerySize
	if len(peers) < nextHeightQuerySize {
		size = len(peers)
	}

	// Pick peers at random to query
	toQuery := make(map[peer.ID]bool)
	for len(toQuery) < size {
		p := peers[rand.Intn(len(peers))]
		if toQuery[p] {
			continue
		}
		toQuery[p] = true
	}

	// Add a peer from each bucket to make sure that as
	// we're syncing we discover any forks that might be
	// out there.
	sm.bucketMtx.RLock()
bucketLoop:
	for _, bucket := range sm.buckets {
		for _, p := range bucket {
			if toQuery[p] {
				continue bucketLoop
			}
		}
		p := bucket[rand.Intn(len(bucket))]
		toQuery[p] = true
	}
	sm.bucketMtx.RUnlock()

	type resp struct {
		p       peer.ID
		blockID types.ID
		height  uint32
	}

	ch := make(chan resp)
	wg := sync.WaitGroup{}
	wg.Add(len(toQuery))
	go func() {
		for p := range toQuery {
			go func(pid peer.ID, w *sync.WaitGroup) {
				defer w.Done()
				h := height
				id, err := sm.chainService.GetBlockID(pid, height)
				if errors.Is(err, ErrNotFound) {
					id, h, err = sm.chainService.GetBest(pid)
				}
				if err != nil {
					sm.network.IncreaseBanscore(pid, 0, 20)
					return
				}
				ch <- resp{
					p:       pid,
					blockID: id,
					height:  h,
				}
			}(p, &wg)
		}
		wg.Wait()
		close(ch)
	}()
	ret := make(map[types.ID]peer.ID)
	count := 0
	for r := range ch {
		if r.height > bestHeight {
			ret[r.blockID] = r.p
		}
		count++
	}
	// If enough peers failed, return error.
	if count < size/2 {
		return nil, errors.New("less than half of peers returned height query response")
	}
	return ret, nil
}

// populatePeerBuckets queries a large number of peers and asks them when their best
// blockID is. If the peers disagree they will be sorted into buckets based on which
// chain they follow.
//
// Note do to the asynchronous nature of the network peers might not report the same
// best blockID even though they are all following the same chain. In this case we
// may still end up putting them into different buckets. This is OK as the buckets
// are only used to add peers to our queries and if there is no fork this won't hurt
// anything.
func (sm *SyncManager) populatePeerBuckets() error {
	peers := sm.syncPeers()
	if len(peers) == 0 {
		return errors.New("no peers to query")
	}
	size := bestHeightQuerySize
	if len(peers) < bestHeightQuerySize {
		size = len(peers)
	}

	toQuery := make(map[peer.ID]bool)
	for len(toQuery) < size {
		p := peers[rand.Intn(len(peers))]
		if toQuery[p] {
			continue
		}
		toQuery[p] = true
	}

	buckets := make(map[types.ID][]peer.ID)

	type resp struct {
		p       peer.ID
		blockID types.ID
		height  uint32
	}

	ch := make(chan resp)
	wg := sync.WaitGroup{}
	wg.Add(len(toQuery))
	go func() {
		for p := range toQuery {
			go func(pid peer.ID, w *sync.WaitGroup) {
				defer w.Done()
				id, height, err := sm.chainService.GetBest(pid)
				if errors.Is(err, ErrNotCurrent) {
					return
				} else if err != nil {
					sm.network.IncreaseBanscore(pid, 0, 20)
					return
				}
				ch <- resp{
					p:       pid,
					blockID: id,
					height:  height,
				}
			}(p, &wg)
		}
		wg.Wait()
		close(ch)
	}()
	count := 0
	for r := range ch {
		count++
		if _, ok := buckets[r.blockID]; !ok {
			buckets[r.blockID] = make([]peer.ID, 0)
		}
		buckets[r.blockID] = append(buckets[r.blockID], r.p)
	}
	// If enough peers failed, return error.
	if count < size/2 {
		return errors.New("less than half of peers returned height query response")
	}
	sm.buckets = buckets
	return nil
}

func (sm *SyncManager) syncToCheckpoints(currentHeight uint32) {
	startHeight := currentHeight + 1
	parent := sm.params.GenesisBlock.ID()
	for z, checkpoint := range sm.params.Checkpoints {
		if currentHeight > checkpoint.Height {
			continue
		}
		if z > 0 {
			parent = sm.params.Checkpoints[z-1].BlockID
		}
		for {
			peers := sm.syncPeers()
			if len(peers) == 0 {
				time.Sleep(time.Second * 5)
				continue
			}
			p := peers[rand.Intn(len(peers))]
			err := sm.syncBlocks(p, startHeight, checkpoint.Height, parent, checkpoint.BlockID, blockchain.BFFastAdd)
			if err != nil {
				log.Debug("Error syncing checkpoints", log.ArgsFromMap(map[string]any{
					"peer":  p,
					"error": err,
				}))
				continue
			}
			break
		}
		startHeight = checkpoint.Height + 1
	}
}

func (sm *SyncManager) downloadEvalWindow(p peer.ID, fromHeight uint32) ([]*blocks.Block, error) {
	headers, err := sm.downloadHeaders(p, fromHeight, fromHeight+evaluationWindow-1)
	if err != nil {
		sm.network.IncreaseBanscore(p, 0, 20)
		return nil, err
	}
	blks := make([]*blocks.Block, 0, len(headers))
	txs, err := sm.downloadBlockTxs(p, fromHeight, fromHeight+evaluationWindow-1)
	if err != nil {
		sm.network.IncreaseBanscore(p, 0, 20)
		return nil, fmt.Errorf("peer %s block download error %s", p, err)
	}
	for i, tx := range txs {
		blks = append(blks, &blocks.Block{
			Header:       headers[i],
			Transactions: tx.Transactions,
		})
	}
	return blks, nil
}

func (sm *SyncManager) syncBlocks(p peer.ID, fromHeight, toHeight uint32, parent, expectedID types.ID, flags blockchain.BehaviorFlags) error {
	headers, err := sm.downloadHeaders(p, fromHeight, toHeight)
	if err != nil {
		sm.network.IncreaseBanscore(p, 0, 20)
		return err
	}
	if headers[len(headers)-1].ID().Compare(expectedID) != 0 {
		sm.network.IncreaseBanscore(p, 101, 0)
		return fmt.Errorf("peer %s returned last header with unexpected ID", p)
	}

	if types.NewID(headers[0].Parent).Compare(parent) != 0 {
		sm.network.IncreaseBanscore(p, 101, 0)
		return fmt.Errorf("peer %s returned frist header with unexpected parent ID", p)
	}
	for i := len(headers) - 1; i > 0; i-- {
		if types.NewID(headers[i].Parent).Compare(headers[i-1].ID()) != 0 {
			sm.network.IncreaseBanscore(p, 101, 0)
			return fmt.Errorf("peer %s returned headers that do not connect", p)
		}
	}

	var (
		blks      []*blocks.Block
		start     = headers[0].Height
		endHeight = headers[len(headers)-1].Height
		headerIdx = 0
	)

	for {
		blks = make([]*blocks.Block, 0, len(headers))

		stop := start + maxBatchSize
		if stop > endHeight {
			stop = endHeight
		}
		txs, err := sm.downloadBlockTxs(p, start, stop)
		if err != nil {
			sm.network.IncreaseBanscore(p, 0, 20)
			return fmt.Errorf("peer %s block download error %s", p, err)
		}
		x := 0
		for i := headerIdx; i < headerIdx+len(txs); i++ {
			blk := &blocks.Block{
				Header:       headers[i],
				Transactions: txs[x].Transactions,
			}
			merkleRoot := blockchain.TransactionsMerkleRoot(blk.Transactions)
			if !bytes.Equal(merkleRoot[:], headers[i].TxRoot) {
				sm.network.IncreaseBanscore(p, 101, 0)
				return fmt.Errorf("peer %s invalid block download merkle root", p.String())
			}
			blks = append(blks, blk)
			x++
		}
		headerIdx += len(txs)

		// Here we are going to extracts all the transactions for the entire batch
		// and validate the proofs and signatures for the whole batch concurrently.
		// This will be faster than validating the proofs and signatures when we
		// serially process the blocks.
		//
		// The proofs and signatures are added to the proof and sig caches so the
		// blockchain will not double validate them.
		if !sm.behavorFlag.HasFlag(blockchain.BFNoValidation) && !sm.behavorFlag.HasFlag(blockchain.BFFastAdd) {
			toValidate := make([]*transactions.Transaction, 0, len(blks))
			for _, blk := range blks {
				toValidate = append(toValidate, blk.Transactions...)
			}
			var (
				proofChan = make(chan error)
				sigChan   = make(chan error)
			)
			defer close(proofChan)
			defer close(sigChan)

			go func() {
				proofChan <- blockchain.NewProofValidator(sm.proofCache, sm.verifier).Validate(toValidate)
			}()
			go func() {
				sigChan <- blockchain.NewSigValidator(sm.sigCache).Validate(toValidate)
			}()
			err = <-proofChan
			if err != nil {
				return fmt.Errorf("error committing block from peer %s: invalid proof in batch", p)
			}
			err = <-sigChan
			if err != nil {
				return fmt.Errorf("error committing block from peer %s: invalid signature in batch", p)
			}
		}
		for _, blk := range blks {
			if err := sm.chain.ConnectBlock(blk, flags); err != nil {
				return fmt.Errorf("error committing block from peer %s. Height: %d, Err: %s", p, blk.Header.Height, err)
			}
		}
		start = stop + 1
		if stop == endHeight {
			break
		}
	}
	return nil
}

func (sm *SyncManager) findForkPoint(currentHeight, toHeight uint32, blockMap map[types.ID]peer.ID) (types.ID, uint32, error) {
	type resp struct {
		p       peer.ID
		blockID types.ID
		err     error
	}
	var (
		startHeight = currentHeight
		midPoint    = currentHeight + (toHeight-currentHeight)/2
		prevMid     = midPoint
		midID       types.ID
	)

	for {
		ch := make(chan resp)
		wg := sync.WaitGroup{}
		wg.Add(len(blockMap))

		go func(getHeight uint32) {
			for _, p := range blockMap {
				go func(pid peer.ID, w *sync.WaitGroup) {
					defer w.Done()
					var (
						id     types.ID
						height uint32
						err    error
					)
					id, err = sm.chainService.GetBlockID(pid, getHeight)
					if errors.Is(err, ErrNotFound) {
						id, height, err = sm.chainService.GetBest(pid)
						if height < startHeight || height >= getHeight {
							err = fmt.Errorf("fork peer %s not returning expected height", pid)
							sm.network.IncreaseBanscore(pid, 101, 0)
						}
					}
					ch <- resp{
						p:       pid,
						blockID: id,
						err:     err,
					}
				}(p, &wg)
			}
			wg.Wait()
			close(ch)
		}(midPoint)
		retMap := make(map[types.ID]struct{})
		for r := range ch {
			if r.err != nil {
				return types.ID{}, 0, r.err
			}
			retMap[r.blockID] = struct{}{}
		}
		if len(retMap) > 1 {
			toHeight = midPoint
			midPoint = currentHeight + ((midPoint - currentHeight) / 2)
		} else {
			currentHeight = midPoint
			midPoint = midPoint + ((toHeight - midPoint) / 2)
			for k := range retMap {
				midID = k
				break
			}
		}
		if prevMid == midPoint {
			return midID, midPoint, nil
		}
		prevMid = midPoint
	}
}

func (sm *SyncManager) downloadHeaders(p peer.ID, startHeight, endHeight uint32) ([]*blocks.BlockHeader, error) {
	headers := make([]*blocks.BlockHeader, 0, endHeight-startHeight)
	height := startHeight
	for {
		ch, err := sm.chainService.GetHeadersStream(p, height)
		if err != nil {
			return nil, err
		}
		count := 0
		for header := range ch {
			headers = append(headers, header)
			height++
			if height > endHeight {
				return headers, nil
			}
			count++
		}
		if count == 0 {
			if len(headers) == 0 {
				return nil, errors.New("peer closed stream without sending any headers")
			}
			break
		}
		if height > endHeight {
			break
		}
	}
	return headers, nil
}

func (sm *SyncManager) downloadBlockTxs(p peer.ID, startHeight, endHeight uint32) ([]*blocks.BlockTxs, error) {
	txs := make([]*blocks.BlockTxs, 0, endHeight-startHeight)
	height := startHeight
	for {
		ch, err := sm.chainService.GetBlockTxsStream(p, height)
		if err != nil {
			return nil, err
		}
		count := 0
		for blockTxs := range ch {
			txs = append(txs, blockTxs)
			height++
			if height > endHeight {
				return txs, nil
			}
			count++
		}
		if count == 0 {
			if len(txs) == 0 {
				return nil, errors.New("peer closed stream without returning any blocktxs")
			}
			break
		}
		if height > endHeight {
			break
		}
	}
	return txs, nil
}

func (sm *SyncManager) waitForPeers() {
	for i := 0; i < 50; i++ {
		n := len(sm.syncPeers())
		if n >= bestHeightQuerySize {
			return
		}
		time.Sleep(time.Millisecond * 100)
	}
}

func (sm *SyncManager) syncPeers() []peer.ID {
	peers := make([]peer.ID, 0, len(sm.network.Host().Network().Peers()))
	for _, p := range sm.network.Host().Network().Peers() {
		protocols, err := sm.network.Host().Peerstore().GetProtocols(p)
		if err != nil {
			continue
		}
		for _, proto := range protocols {
			if proto == sm.params.ProtocolPrefix+ChainServiceProtocol+ChainServiceProtocolVersion {
				peers = append(peers, p)
				break
			}
		}
	}
	return peers
}
