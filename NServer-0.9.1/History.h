#ifndef _History_h_
#define _History_h_

#include <string>
#include <vector>

#include "artnum.h"

/*
 * The spool's history: which groups of the spool hold an article with a
 * given Message-ID, and under which number. It lets ARTICLE <message-id>
 * and the other message-ID commands serve an article from the spool, and
 * fetch a crossposted article only once. Modelled on go-pugleaf's
 * history (and INN's): the key is md5(message-id) in hex; its first hex
 * digit picks one of 16 shard files <spool>/.history/0 ... f, the next
 * two one of the 256 buckets of that file. A record is the message-ID
 * and its locations, "group:number,group:number,...", a group at most
 * once. The routing is part of the file format and can't change.
 *
 * Each shard is an NVHash (with the container's file locks, so session
 * processes contend per shard) holding, in bucket 0, a record that marks
 * the format. A shard that is damaged or of another format is moved
 * aside (to <name>.bad) and starts empty: the history is a cache of what
 * the spool holds, and every lookup checks the location against the
 * spool anyway.
 *
 * Without setspooldir() every call does nothing (and lookup finds
 * nothing), so programs that don't need it don't touch the files.
 */
class History {
  public:
	enum { SHARDS = 16, BUCKETS = 256 };

	struct Location {
		std::string group;
		artnum_t nbr;
	};

	// The spool directory; <spooldir>/.history holds the shards.
	static void setspooldir(const char *spooldir);
	static bool enabled(void);

	// Records that group holds the article msgid as number nbr
	// (replaces an older number of the same group).
	static void add(const char *msgid, const char *group, artnum_t nbr);
	// Like add, but first looks (with a shared lock) whether the entry
	// is there already: for articles read from the spool, so a spool of
	// an older version gets into the history as clients read it.
	static void note(const char *msgid, const char *group, artnum_t nbr);
	// Removes the location of msgid in group, and the record when no
	// location is left.
	static void remove(const char *msgid, const char *group);
	// The locations of msgid; their number.
	static int lookup(const char *msgid, std::vector<Location> &locs);

	/*
	 * Collects add and remove calls until the matching commit(), which
	 * applies them with one lock per shard (setover of a whole OVER
	 * reply, freerange of a range). Nests.
	 */
	static void begin(void);
	static void commit(void);

	/*
	 * Drops every location for which keep(group, arg) is false, and
	 * records without locations (newscacheclean: groups whose database
	 * is gone). Returns the number of locations dropped.
	 */
	static long prune(bool (*keep) (const std::string & group, void *arg),
			  void *arg);

	// md5 of msgid, the shard (0..15) and the bucket (0..255).
	static void route(const char *msgid, unsigned char digest[16],
			  int *shard, int *bucket);
};

/*
 * History::begin() and commit() for a block, also when it is left by an
 * exception.
 */
class HistoryBatch {
  public:
	HistoryBatch() {
		History::begin();
	}
	~HistoryBatch() {
		History::commit();
	}
};

#endif

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
