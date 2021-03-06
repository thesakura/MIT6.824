package shardkv

//
// client code to talk to a sharded key/value service.
//
// the client first talks to the shardmaster to find out
// the assignment of shards (keys) to groups, and then
// talks to the group that holds the key's shard.
//

import "6.824/labrpc"
import "crypto/rand"
import "math/big"
import "6.824/shardctrler"
import "time"
// import "fmt"

//
// which shard is a key in?
// please use this function,
// and please do not change it.
//
//寻找key的内容在哪个shard中
func key2shard(key string) int {
	shard := 0
	if len(key) > 0 {
		shard = int(key[0])
	}
	shard %= shardctrler.NShards
	return shard
}

func nrand() int64 {
	max := big.NewInt(int64(1) << 62)
	bigx, _ := rand.Int(rand.Reader, max)
	x := bigx.Int64()
	return x
}

type Clerk struct {
	sm       *shardctrler.Clerk
	config   shardctrler.Config
	// string为类型，然后返回labrpc.ClientEnd的可以发送RPC的类型
	make_end func(string) *labrpc.ClientEnd

	id 	   int64
	seqnum int64
}

//
// the tester calls MakeClerk.
//
// masters[] is needed to call shardmaster.MakeClerk().
//
// make_end(servername) turns a server name from a
// Config.Groups[gid][i] into a labrpc.ClientEnd on which you can
// send RPCs.
//
func MakeClerk(masters []*labrpc.ClientEnd, make_end func(string) *labrpc.ClientEnd) *Clerk {
	ck := new(Clerk)
	ck.sm = shardctrler.MakeClerk(masters)
	ck.make_end = make_end

	ck.id = nrand()
	ck.seqnum = 0

	return ck
}

//对于每个key，获取它所处的shard，然后对负责这个shard的group上的每个server，进行请求，
// 如果成功，或者没有这个key，返回reply，如果group错误或者正在传输中直接退出
//同时每隔一段时间向ctrler节点查询最新配置
// fetch the current value for a key.
// returns "" if the key does not exist.
// keeps trying forever in the face of all other errors.
// You will have to modify this function.
//
func (ck *Clerk) Get(key string) string {
	ck.seqnum += 1

	args := GetArgs{Key:key, CltId:ck.id, SeqNum:ck.seqnum}
	//对于key所处的shard的group的每个server，发送get请求
	for {
		shard := key2shard(key)
		gid := ck.config.Shards[shard]
		if servers, ok := ck.config.Groups[gid]; ok {
			// 构建客户端，发起RPC，try each server for the shard.
			
			for si := 0; si < len(servers); si++ {
				srv := ck.make_end(servers[si])
				var reply GetReply
				ok := srv.Call("ShardKV.Get", &args, &reply)
				if ok && reply.WrongLeader == false && (reply.Err == OK || reply.Err == ErrNoKey) {
					return reply.Value
				}
				if ok && (reply.Err == ErrWrongGroup) {  // shard in other groups
					break
				}
				if ok && (reply.Err == ErrInTransit) {  // during transition
					break
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
		// 请求最新的配置
		ck.config = ck.sm.Query(-1)
	}

	return ""
}

//
// shared by Put and Append.
// You will have to modify this function.
//
func (ck *Clerk) PutAppend(key string, value string, op string) {
	ck.seqnum += 1

	args := PutAppendArgs{Key:key, Value:value, Op:op, CltId:ck.id, SeqNum:ck.seqnum}

	for {
		shard := key2shard(key)
		gid := ck.config.Shards[shard]
		if servers, ok := ck.config.Groups[gid]; ok {
			for si := 0; si < len(servers); si++ {
				srv := ck.make_end(servers[si])
				var reply PutAppendReply
				ok := srv.Call("ShardKV.PutAppend", &args, &reply)
				if ok && reply.WrongLeader == false && reply.Err == OK {
					return
				}
				if ok && reply.Err == ErrWrongGroup {  // shard in other groups
					break
				}
				if ok && (reply.Err == ErrInTransit) {  // during transition
					break
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
		// ask master for the latest configuration.
		ck.config = ck.sm.Query(-1)
	}
}

func (ck *Clerk) Put(key string, value string) {
	ck.PutAppend(key, value, "Put")
}
func (ck *Clerk) Append(key string, value string) {
	ck.PutAppend(key, value, "Append")
}