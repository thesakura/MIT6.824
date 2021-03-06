package kvraft

import (
	"encoding/gob"
	"6.824/labrpc"
	"log"
	"6.824/raft"
	"sync"
	"time"
	"bytes"
	"reflect"
	// "fmt"
)

const Debug = 0

const MaxRaftFactor = 0.8

const ResChanSize = 1
const ResChanTimeout = 1000

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug > 0 {
		log.Printf(format, a...)
	}
	return
}


type Op struct {
	Request string  // "Put", "Append", "Get"
	Key     string  
	Value   string  // set to "" for Get request
	CltId   int64   // client unique identifier
	SeqNum  int64
}

type ReplyRes struct {
	Value   string 
	InOp    Op
}

type KVServer struct {
	mu      sync.Mutex
	me      int
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg

	maxraftstate int // snapshot if log grows this big

	kvdb    map[string]string
	rfidx   int 
	cltsqn  map[int64]int64  // sequence number log for each client

	chanMapMu  sync.Mutex
	resChanMap map [int] chan ReplyRes // communication from applyDb to clients

	// close goroutine
	killIt chan bool
}

func (kv *KVServer) createResChan(cmtidx int) {
	kv.chanMapMu.Lock()
	if kv.resChanMap[cmtidx] == nil {
		kv.resChanMap[cmtidx] = make(chan ReplyRes, ResChanSize)
	}
	kv.chanMapMu.Unlock()
}

func (kv *KVServer) ApplyDb() {
	for{
		select {
			case <- kv.killIt:
				return
			default:
				applymsg := <- kv.applyCh

				kv.mu.Lock()

				if !applymsg.CommandValid {

					r := bytes.NewBuffer(applymsg.Snapshot)
					d := gob.NewDecoder(r)
					kv.kvdb = make(map[string]string)
					d.Decode(&kv.kvdb)
					d.Decode(&kv.rfidx)
					d.Decode(&kv.cltsqn)

				} else {
					op,ok := applymsg.Command.(Op)
					if !ok{
						kv.mu.Unlock()
						continue
					}
					kv.rfidx = applymsg.CommandIndex

					kv.createResChan(applymsg.CommandIndex)

					if val, ok := kv.cltsqn[op.CltId]; !ok || op.SeqNum > val {

						kv.cltsqn[op.CltId] = op.SeqNum
						if op.Request == "Put" {
							kv.kvdb[op.Key] = op.Value
						} else if op.Request == "Append" {
							kv.kvdb[op.Key] += op.Value
						} else if op.Request == "Get" {
							// dummy
						}
					}

					kv.chanMapMu.Lock()
					resCh := kv.resChanMap[applymsg.CommandIndex]
					kv.chanMapMu.Unlock()

					select{
						case <- resCh:
							// flush the channel
						default:
							// no need to flush
					}

					resCh <- ReplyRes{Value:kv.kvdb[op.Key], InOp:op}
				}

				kv.mu.Unlock()

				go kv.CheckSnapshot()
			}
	}
}

func (kv *KVServer) CheckSnapshot() {
	if float64(kv.rf.GetRaftStateSize()) / float64(kv.maxraftstate) > MaxRaftFactor {
		kv.SaveSnapshot()
	}
}

func (kv *KVServer) SaveSnapshot() {
	kv.mu.Lock()
	
	w := new(bytes.Buffer)
	e := gob.NewEncoder(w)
	e.Encode(kv.kvdb)
	e.Encode(kv.rfidx)
	e.Encode(kv.cltsqn)
	data := w.Bytes()

	kvrfidx := kv.rfidx  // preserve this value outside the lock

	kv.mu.Unlock()  // has to unlock here, otherwise deadlock


	kv.rf.Snapshot( kvrfidx,data)
}

func (kv *KVServer) ReadSnapshot(data []byte) {
	kv.mu.Lock()
	defer kv.mu.Unlock()

	r := bytes.NewBuffer(data)
	d := gob.NewDecoder(r)
	d.Decode(&kv.kvdb)
	d.Decode(&kv.rfidx)
	d.Decode(&kv.cltsqn)
}

func (kv *KVServer) Get(args *GetArgs, reply *GetReply) {

	op := Op{Request: "Get", Key: args.Key, Value: "", CltId:args.CltId, SeqNum: args.SeqNum}
	
	cmtidx, _, isLeader := kv.rf.Start(op)

	if !isLeader{
		reply.WrongLeader = true
		return
	}

	kv.createResChan(cmtidx)

	kv.chanMapMu.Lock()
	resCh := kv.resChanMap[cmtidx]
	kv.chanMapMu.Unlock()

	select{
		case res := <- resCh:
			if reflect.DeepEqual(op, res.InOp) {
				reply.Value = res.Value
			} else{
				reply.WrongLeader = true
			}
		case <- time.After(ResChanTimeout * time.Millisecond): // RPC timeout
			reply.WrongLeader = true
	}
}

func (kv *KVServer) PutAppend(args *PutAppendArgs, reply *PutAppendReply) {

	op := Op{Request: args.Op, Key: args.Key, Value: args.Value, CltId:args.CltId, SeqNum: args.SeqNum}

	cmtidx, _, isLeader := kv.rf.Start(op)

	if !isLeader{
		reply.WrongLeader = true
		return
	}

	kv.createResChan(cmtidx)

	kv.chanMapMu.Lock()
	resCh := kv.resChanMap[cmtidx]
	kv.chanMapMu.Unlock()

	select{
		case res := <- resCh:
			if reflect.DeepEqual(op, res.InOp) {
				// dummy
			} else{
				reply.WrongLeader = true
			}
		case <- time.After(ResChanTimeout * time.Millisecond): // RPC timeout
			reply.WrongLeader = true
	}
}

//
// the tester calls Kill() when a RaftKV instance won't
// be needed again. you are not required to do anything
// in Kill(), but it might be convenient to (for example)
// turn off debug output from this instance.
//
func (kv *KVServer) Kill() {
	kv.rf.Kill()
	close(kv.killIt)
}

//
// servers[] contains the ports of the set of
// servers that will cooperate via Raft to
// form the fault-tolerant key/value service.
// me is the index of the current server in servers[].
// the k/v server should store snapshots with persister.SaveSnapshot(),
// and Raft should save its state (including log) with persister.SaveRaftState().
// the k/v server should snapshot when Raft's saved state exceeds maxraftstate bytes,
// in order to allow Raft to garbage-collect its log. if maxraftstate is -1,
// you don't need to snapshot.
// StartKVServer() must return quickly, so it should start goroutines
// for any long-running work.
//
func StartKVServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister, maxraftstate int) *KVServer {
	// call gob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	gob.Register(Op{})
	gob.Register(GetArgs{})
	gob.Register(GetReply{})
	gob.Register(PutAppendArgs{})
	gob.Register(PutAppendReply{})

	kv := new(KVServer)
	kv.me = me
	kv.maxraftstate = maxraftstate

	kv.applyCh = make(chan raft.ApplyMsg)
	kv.rf = raft.Make(servers, me, persister, kv.applyCh)

	kv.kvdb = make(map[string]string)
	kv.rfidx = 0
	kv.cltsqn = make(map[int64]int64)

	kv.resChanMap = make(map [int] chan ReplyRes)

	kv.killIt = make(chan bool)

	kv.ReadSnapshot(persister.ReadSnapshot())

	go kv.ApplyDb()

	return kv
}
