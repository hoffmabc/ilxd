// Copyright (c) 2022 The illium developers
// Use of this source code is governed by an MIT
// license that can be found in the LICENSE file.

package blockchain

import (
	"github.com/ipfs/go-datastore"
	"github.com/project-illium/ilxd/models"
	"github.com/project-illium/ilxd/models/blocks"
	"github.com/project-illium/ilxd/repo"
)

const blockIndexCacheSize = 1000

// blockNode represents a block in the chain. It stores the hash
// and height as well as links to the parent and child making it
// possible to traverse the chain back and forward from this blocknode.
type blockNode struct {
	ds      repo.Datastore
	blockID models.ID
	height  uint32
	parent  *blockNode
	child   *blockNode
}

// ID returns the block ID of this blocknode.
func (bn *blockNode) ID() models.ID {
	return bn.blockID
}

// Header returns the header for this blocknode. The header is
// loaded from the database.
func (bn *blockNode) Header() (*blocks.BlockHeader, error) {
	return dsFetchHeader(bn.ds, bn.blockID)
}

// Block returns the full block for this blocknode. The block
// is loaded from the databse.
func (bn *blockNode) Block() (*blocks.Block, error) {
	return dsFetchBlock(bn.ds, bn.blockID)
}

// Height returns the height from this node.
func (bn *blockNode) Height() uint32 {
	return bn.height
}

// Parent returns the parent blocknode for this block. If the
// parent is cached it will be returned from cache. Otherwise it
// will be loaded from the db.
func (bn *blockNode) Parent() (*blockNode, error) {
	if bn.parent != nil {
		return bn.parent, nil
	}
	if bn.height == 0 {
		return nil, nil
	}
	parentID, err := dsFetchBlockIDFromHeight(bn.ds, bn.height-1)
	if err != nil {
		return nil, err
	}
	parent := &blockNode{
		ds:      bn.ds,
		blockID: parentID,
		height:  bn.height - 1,
		parent:  nil,
		child:   nil,
	}
	bn.parent = parent
	return parent, nil
}

// Child returns the child blocknode for this block. If the
// child is cached it will be returned from cache. Otherwise it
// will be loaded from the db.
func (bn *blockNode) Child() (*blockNode, error) {
	if bn.child != nil {
		return bn.child, nil
	}
	childID, err := dsFetchBlockIDFromHeight(bn.ds, bn.height+1)
	if err != nil {
		return nil, err
	}
	child := &blockNode{
		ds:      bn.ds,
		blockID: childID,
		height:  bn.height + 1,
		parent:  nil,
		child:   nil,
	}
	bn.child = child
	return child, nil
}

type blockIndex struct {
	ds            repo.Datastore
	tip           *blockNode
	cacheByID     map[models.ID]*blockNode
	cacheByHeight map[uint32]*blockNode
}

// NewBlockIndex returns a new blockIndex.
func NewBlockIndex(ds repo.Datastore) *blockIndex {
	return &blockIndex{
		ds:            ds,
		cacheByID:     make(map[models.ID]*blockNode),
		cacheByHeight: make(map[uint32]*blockNode),
	}
}

// Init loads the current index state from the database and
// fill the cache for quick access.
func (bi *blockIndex) Init() error {
	tip, err := dsFetchBlockIndexState(bi.ds)
	if err != nil {
		return err
	}
	bi.tip = tip

	for i := 0; i < blockIndexCacheSize; i++ {
		parent, err := tip.Parent()
		if err != nil {
			return err
		}
		if parent == nil {
			break
		}
	}
	return nil
}

// Tip returns the blocknode at the tip of the chain.
func (bi *blockIndex) Tip() *blockNode {
	return bi.tip
}

// Commit commits the current tip of the index to the database.
func (bi *blockIndex) Commit(dbtx datastore.Txn) error {
	return dsPutBlockIndexState(dbtx, bi.tip)
}

// ExtendIndex updates the tip of the index with the provided header.
// This does NOT commit the change to the database. For that you must
// call Commit().
func (bi *blockIndex) ExtendIndex(header *blocks.BlockHeader) {
	node := &blockNode{
		ds:      bi.ds,
		blockID: header.ID(),
		height:  header.Height,
		parent:  bi.tip,
		child:   nil,
	}
	bi.tip.child = node
	bi.tip = node
	bi.cacheByID[node.blockID] = node
	bi.cacheByHeight[node.height] = node
	bi.limitCache()
}

// GetNodeByHeight returns a blockNode at the provided height. It will be
// returned from cache if it exists, otherwise it will be loaded from the
// database.
func (bi *blockIndex) GetNodeByHeight(height uint32) (*blockNode, error) {
	node, ok := bi.cacheByHeight[height]
	if ok {
		return node, nil
	}

	blockID, err := dsFetchBlockIDFromHeight(bi.ds, height)
	if err != nil {
		return nil, err
	}
	node = &blockNode{
		ds:      bi.ds,
		blockID: blockID,
		height:  height,
		parent:  nil,
		child:   nil,
	}
	parent, ok := bi.cacheByHeight[height-1]
	if ok {
		node.parent = parent
	}
	child, ok := bi.cacheByHeight[height+1]
	if ok {
		node.child = child
	}
	bi.cacheByID[blockID] = node
	bi.cacheByHeight[height] = node
	bi.limitCache()
	return node, nil
}

// GetNodeByID returns a blockNode for the provided ID. It will be
// returned from cache if it exists, otherwise it will be loaded from the
// database.
func (bi *blockIndex) GetNodeByID(blockID models.ID) (*blockNode, error) {
	node, ok := bi.cacheByID[blockID]
	if ok {
		return node, nil
	}

	header, err := dsFetchHeader(bi.ds, blockID)
	if err != nil {
		return nil, err
	}
	node = &blockNode{
		ds:      bi.ds,
		blockID: blockID,
		height:  header.Height,
		parent:  nil,
		child:   nil,
	}
	parent, ok := bi.cacheByHeight[header.Height-1]
	if ok {
		node.parent = parent
	}
	child, ok := bi.cacheByHeight[header.Height+1]
	if ok {
		node.child = child
	}
	bi.cacheByID[blockID] = node
	bi.cacheByHeight[header.Height] = node
	bi.limitCache()
	return node, nil
}

func (bi *blockIndex) limitCache() {
	if len(bi.cacheByID) > blockIndexCacheSize {
		for id, node := range bi.cacheByID {
			if node.parent != nil {
				node.parent.child = nil
			}
			if node.child != nil {
				node.child.parent = nil
			}

			delete(bi.cacheByID, id)
			break
		}
	}
	if len(bi.cacheByHeight) > blockIndexCacheSize {
		for height, node := range bi.cacheByHeight {
			if node.parent != nil {
				node.parent.child = nil
			}
			if node.child != nil {
				node.child.parent = nil
			}
			delete(bi.cacheByHeight, height)
			break
		}
	}
}
