#include <errno.h>
#include <stdio.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

#include <algorithm>
#include <string>
#include <vector>

#include "Error.h"
#include "Logger.h"
#include "NVHash.h"
#include "History.h"
#include "md5.h"

using namespace std;

/*
 * Record layout in a shard (the data of an NVHash record):
 *   16 bytes   md5(message-id)
 *   n+1 bytes  message-id, NUL
 *   m+1 bytes  locations "group:number,...", NUL
 * Bucket 0 of every shard also holds the format record: 16 zero bytes,
 * an empty message-id, and HISTORY_MAGIC.
 */
#define HISTORY_MAGIC "NewsCache history 1: md5 routing 16x256"

namespace {

struct Corrupt {
};

class Shard:public NVHash {
  public:
	string path;
	pid_t pid;		// process that opened it (0: not open)
	bool usable;

	Shard():NVHash(), pid(0), usable(false) {
	}

	using NVHash::lock;
	using NVHash::Record;

	void check(nvoff_t o) {
		if (o < 16 || o % sizeof(unsigned long) != 0 ||
		    o + sizeof(Record) > mem_sz)
			throw Corrupt();
		Record *r = o2r(o);
		if (r->szdata > mem_sz - o - sizeof(Record))
			throw Corrupt();
	}

	// The record of (digest, msgid) in bucket b and its predecessor,
	// or NULL. Needs a lock.
	Record *sfind(int b, const unsigned char *dg, const char *msgid,
		      Record ** pprev) {
		if (!hashtab || hashsz != History::BUCKETS)
			throw Corrupt();
		nvoff_t t = hashtab[b];
		if (!t)
			return NULL;
		check(t);
		Record *tail = o2r(t), *prev = tail, *r;
		size_t n = 0, max = mem_sz / sizeof(Record);
		size_t idlen = strlen(msgid);
		do {
			check(prev->next);
			r = o2r(prev->next);
			if (r->szdata >= 16 + idlen + 2 &&
			    memcmp(r->datap(), dg, 16) == 0 &&
			    memcmp(r->datap() + 16, msgid, idlen + 1) == 0) {
				if (pprev)
					*pprev = prev;
				return r;
			}
			prev = r;
			if (++n > max)
				throw Corrupt();
		} while (r != tail);
		return NULL;
	}

	// Calls f(msgid, locations, arg) for every record (not the format
	// record), checking the links like sfind. Needs a lock.
	void each(void (*f) (const string &, const string &, void *),
		  void *arg) {
		if (!hashtab || hashsz != History::BUCKETS)
			throw Corrupt();
		size_t max = mem_sz / sizeof(Record), n = 0;
		for (int b = 0; b < History::BUCKETS; b++) {
			nvoff_t t = hashtab[b];
			if (!t)
				continue;
			check(t);
			Record *tail = o2r(t), *prev = tail, *r;
			do {
				check(prev->next);
				r = o2r(prev->next);
				if (r->szdata >= 18) {
					const char *id = r->datap() + 16,
					    *e = r->datap() + r->szdata;
					size_t idl = strnlen(id, e - id);
					if (idl > 0 && id + idl < e)
						f(string(id, idl), locations(r),
						  arg);
				}
				prev = r;
				if (++n > max)
					throw Corrupt();
			} while (r != tail);
		}
	}

	// The locations string of a record ("" if it has none).
	string locations(Record * r) {
		const char *p = r->datap() + 16, *e = r->datap() + r->szdata;
		p += strnlen(p, e - p) + 1;
		if (p >= e)
			return "";
		return string(p, strnlen(p, e - p));
	}

	void sunlink(int b, Record * r, Record * prev) {
		if (r == prev) {
			hashtab[b] = 0;
		} else {
			prev->next = r->next;
			if (o2r(hashtab[b]) == r)
				hashtab[b] = r2o(prev);
		}
		nvfree(r2o(r));
	}

	void sput(int b, const unsigned char *dg, const char *msgid,
		  const string & locs) {
		string data((const char *) dg, 16);
		data += msgid;
		data += '\0';
		data += locs;
		data += '\0';
		sadd(b, data.data(), data.size());
	}

	void open_checked(void) {
		static const unsigned char zero[16] = { 0 };
		NVHash::open(path.c_str(), History::BUCKETS);
		lock(ExclLock);
		try {
			if (gethashsz() != History::BUCKETS)
				throw Corrupt();
			Record *m = sfind(0, zero, "", NULL);
			if (!m) {
				if (!sis_empty())
					throw Corrupt();
				sput(0, zero, "", HISTORY_MAGIC);
			} else if (locations(m) != HISTORY_MAGIC) {
				throw Corrupt();
			}
		}
		catch(...) {
			lock(UnLock);
			throw;
		}
		lock(UnLock);
	}
};

string spool;			// "" = disabled
Shard shards[History::SHARDS];

struct Op {
	bool add;
	string msgid, group;
	artnum_t nbr;
};
int batch_depth = 0;
vector < Op > batch;

/*
 * Moves a damaged shard aside and starts it empty.
 */
void reset_shard(Shard & s, const char *why)
{
	slog.p(Logger::Error) << "history: " << s.path << " " << why
	    << ", moved to " << s.path << ".bad and started anew\n";
	try {
		s.close();
	}
	catch(...) {
	}
	rename(s.path.c_str(), (s.path + ".bad").c_str());
	s.pid = 0;
	s.usable = false;
}

// The shard, open in this process, or NULL if it can't be used.
Shard *shard(int n)
{
	Shard & s = shards[n];
	if (spool.empty())
		return NULL;
	if (s.pid == getpid())
		return s.usable ? &s : NULL;
	// first use in this process (or after a fork)
	if (s.pid != 0) {
		try {
			s.close();
		}
		catch(...) {
		}
	}
	s.pid = getpid();
	s.usable = false;
	char name[8];
	snprintf(name, sizeof(name), "%x", n);
	s.path = spool + "/.history/" + name;
	for (int attempt = 0; attempt < 2; attempt++) {
		try {
			s.open_checked();
			s.usable = true;
			return &s;
		}
		catch(Corrupt &) {
			reset_shard(s, "is damaged or of another format");
		}
		catch(Error & e) {
			// make_current renames a damaged file to .bad itself
			slog.p(Logger::Error) << "history: cannot open "
			    << s.path << ": " << e._errtext << "\n";
			try {
				s.close();
			}
			catch(...) {
			}
		}
		catch(...) {
			slog.p(Logger::Error) << "history: cannot open "
			    << s.path << "\n";
			try {
				s.close();
			}
			catch(...) {
			}
		}
		s.pid = getpid();
	}
	return NULL;
}

void md5digest(const char *msgid, unsigned char dg[16])
{
	MD5_CTX ctx;
	MD5Init(&ctx);
	MD5Update(&ctx, (const unsigned char *) msgid, strlen(msgid));
	MD5Final(dg, &ctx);
}

// "group:number" tokens of a locations string.
vector < History::Location > parse(const string & locs)
{
	vector < History::Location > v;
	string::size_type i = 0;
	while (i < locs.size()) {
		string::size_type e = locs.find(',', i);
		if (e == string::npos)
			e = locs.size();
		string tok = locs.substr(i, e - i);
		string::size_type c = tok.rfind(':');
		artnum_t n;
		if (c != string::npos && c > 0 &&
		    parse_artnum(tok.c_str() + c + 1, &n)) {
			History::Location l;
			l.group = tok.substr(0, c);
			l.nbr = n;
			v.push_back(l);
		}
		i = e + 1;
	}
	return v;
}

string unparse(const vector < History::Location > &v)
{
	string s;
	char buf[32];
	for (size_t i = 0; i < v.size(); i++) {
		if (i)
			s += ',';
		snprintf(buf, sizeof(buf), "%llu",
			 (unsigned long long) v[i].nbr);
		s += v[i].group + ':' + buf;
	}
	return s;
}

bool valid_group(const char *g)
{
	return *g && !strpbrk(g, ":,\r\n\t ") && strlen(g) < 512;
}

bool valid_msgid(const char *m)
{
	size_t n = strlen(m);
	return n >= 3 && n < 1024 && m[0] == '<' && m[n - 1] == '>' &&
	    !strpbrk(m, "\r\n");
}

// Applies one add or remove to shard s (which must be locked).
void apply(Shard & s, int b, const unsigned char *dg, const Op & op)
{
	Shard::Record *prev, *r = s.sfind(b, dg, op.msgid.c_str(), &prev);
	vector < History::Location > v;
	if (r)
		v = parse(s.locations(r));
	vector < History::Location > old = v;
	size_t i;
	for (i = 0; i < v.size() && v[i].group != op.group; i++);
	if (op.add) {
		if (i < v.size()) {
			if (v[i].nbr == op.nbr)
				return;
			v[i].nbr = op.nbr;
		} else {
			History::Location l;
			l.group = op.group;
			l.nbr = op.nbr;
			v.push_back(l);
		}
	} else {
		if (i == v.size())
			return;
		v.erase(v.begin() + i);
	}
	if (r)
		s.sunlink(b, r, prev);
	if (!v.empty())
		s.sput(b, dg, op.msgid.c_str(), unparse(v));
}

// Applies ops (all for shard n) under one lock.
void apply_all(int n, const vector < const Op *>&ops)
{
	Shard *s = shard(n);
	if (!s)
		return;
	try {
		s->lock(NVcontainer::ExclLock);
		try {
			for (size_t i = 0; i < ops.size(); i++) {
				unsigned char dg[16];
				int sh, b;
				History::route(ops[i]->msgid.c_str(), dg, &sh, &b);
				apply(*s, b, dg, *ops[i]);
			}
		}
		catch(...) {
			s->lock(NVcontainer::UnLock);
			throw;
		}
		s->lock(NVcontainer::UnLock);
	}
	catch(Corrupt &) {
		reset_shard(*s, "is damaged");
	}
	catch(Error & e) {
		slog.p(Logger::Error) << "history: " << s->path << ": "
		    << e._errtext << "\n";
		reset_shard(*s, "failed");
	}
}

void queue_op(bool add, const char *msgid, const char *group, artnum_t nbr)
{
	if (spool.empty() || !msgid || !group || !valid_msgid(msgid) ||
	    !valid_group(group))
		return;
	Op op;
	op.add = add;
	op.msgid = msgid;
	op.group = group;
	op.nbr = nbr;
	batch.push_back(op);
	if (batch_depth == 0)
		History::commit();
}

struct PruneCtx {
	bool (*keep) (const string & group, void *arg);
	void *arg;
	vector < Op > ops;
};

void prune_one(const string & msgid, const string & locs, void *a)
{
	PruneCtx *ctx = (PruneCtx *) a;
	vector < History::Location > v = parse(locs);
	for (size_t i = 0; i < v.size(); i++) {
		if (!ctx->keep(v[i].group, ctx->arg)) {
			Op op;
			op.add = false;
			op.msgid = msgid;
			op.group = v[i].group;
			op.nbr = 0;
			ctx->ops.push_back(op);
		}
	}
}

}				// namespace

void History::route(const char *msgid, unsigned char dg[16], int *shard,
		    int *bucket)
{
	md5digest(msgid, dg);
	// hex digit 0 -> shard, hex digits 1 and 2 -> bucket
	*shard = dg[0] >> 4;
	*bucket = ((dg[0] & 0xf) << 4) | (dg[1] >> 4);
}

void History::setspooldir(const char *spooldir)
{
	if (!spool.empty() || !spooldir || !*spooldir)
		return;
	string dir = string(spooldir) + "/.history";
	if (mkdir(dir.c_str(), 0755) != 0 && errno != EEXIST) {
		slog.p(Logger::Error) << "history: cannot create " << dir
		    << ": " << strerror(errno) << "\n";
		return;
	}
	spool = spooldir;
}

bool History::enabled(void)
{
	return !spool.empty();
}

void History::add(const char *msgid, const char *group, artnum_t nbr)
{
	queue_op(true, msgid, group, nbr);
}

void History::note(const char *msgid, const char *group, artnum_t nbr)
{
	if (spool.empty() || !msgid || !group || !valid_msgid(msgid) ||
	    !valid_group(group))
		return;
	vector < Location > v;
	lookup(msgid, v);
	for (size_t i = 0; i < v.size(); i++)
		if (v[i].group == group && v[i].nbr == nbr)
			return;
	add(msgid, group, nbr);
}

void History::remove(const char *msgid, const char *group)
{
	queue_op(false, msgid, group, 0);
}

void History::begin(void)
{
	batch_depth++;
}

void History::commit(void)
{
	if (batch_depth > 0 && --batch_depth > 0)
		return;
	if (batch.empty())
		return;
	vector < Op > ops;
	ops.swap(batch);
	vector < const Op *>per[SHARDS];
	for (size_t i = 0; i < ops.size(); i++) {
		unsigned char dg[16];
		int sh, b;
		route(ops[i].msgid.c_str(), dg, &sh, &b);
		per[sh].push_back(&ops[i]);
	}
	for (int n = 0; n < SHARDS; n++) {
		if (!per[n].empty())
			apply_all(n, per[n]);
	}
}

int History::lookup(const char *msgid, vector < Location > &locs)
{
	locs.clear();
	if (spool.empty() || !valid_msgid(msgid))
		return 0;
	unsigned char dg[16];
	int sh, b;
	route(msgid, dg, &sh, &b);
	Shard *s = shard(sh);
	if (!s)
		return 0;
	try {
		s->lock(NVcontainer::ShrdLock);
		try {
			Shard::Record *r = s->sfind(b, dg, msgid, NULL);
			if (r)
				locs = parse(s->locations(r));
		}
		catch(...) {
			s->lock(NVcontainer::UnLock);
			throw;
		}
		s->lock(NVcontainer::UnLock);
	}
	catch(Corrupt &) {
		reset_shard(*s, "is damaged");
		locs.clear();
	}
	catch(Error & e) {
		slog.p(Logger::Error) << "history: " << s->path << ": "
		    << e._errtext << "\n";
		reset_shard(*s, "failed");
		locs.clear();
	}
	return locs.size();
}

long History::prune(bool (*keep) (const string & group, void *arg),
		    void *arg)
{
	long dropped = 0;
	if (spool.empty())
		return 0;
	for (int n = 0; n < SHARDS; n++) {
		Shard *s = shard(n);
		if (!s)
			continue;
		// Collect first, then apply: the records move when changed.
		PruneCtx ctx;
		ctx.keep = keep;
		ctx.arg = arg;
		try {
			s->lock(NVcontainer::ShrdLock);
			try {
				s->each(prune_one, &ctx);
			}
			catch(...) {
				s->lock(NVcontainer::UnLock);
				throw;
			}
			s->lock(NVcontainer::UnLock);
		}
		catch(...) {
			reset_shard(*s, "is damaged");
			continue;
		}
		vector < Op > &ops = ctx.ops;
		vector < const Op *>p;
		for (size_t i = 0; i < ops.size(); i++)
			p.push_back(&ops[i]);
		apply_all(n, p);
		dropped += ops.size();
	}
	return dropped;
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
