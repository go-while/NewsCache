#ifndef _ActiveDB_h_
#define _ActiveDB_h_

#include <ctype.h>

#include <iostream>

#include "NVcontainer.h"
#include "NSError.h"
#include "GroupInfo.h"

#include "VirtualIterator.h"

/*
 * Which lines of an active list ActiveDB::read keeps.
 */
class ActiveFilter {
  public:
	virtual ~ActiveFilter() {}
	// line: "group last first flags"
	virtual bool keep(const char *line) const = 0;
};

class ActiveDB {
  protected:
	ActiveDB();

  public:
	virtual ~ ActiveDB();
	// Used for set
	enum {
		update_only = 0x01,
		// An existing record keeps its posting flag (y, m, n, ...):
		// a GROUP reply has numbers only, the flag comes from the
		// active list. Read and written under one exclusive lock.
		keep_flag = 0x02,
		// gi's numbers are from the time in its mtime, when they were
		// asked for: an existing record asked for later keeps its
		// numbers and mtime, and gi gets them. A slow LIST ACTIVE (or
		// a GROUP reply stored late) put older numbers over a newer
		// GROUP reply, and the group's last number went back. In the
		// same second the higher last number stays. A record whose
		// time is still to come (the clock was put back) is replaced.
		keep_newer = 0x04
	};

	enum {
		m_active, m_times
	};
	// Reserved timestamps: a first refresh is incomplete, or an established
	// list was invalidated and should be refreshed.
	enum { FIRST_REFRESH_PENDING = 1, INVALIDATED = 2 };

	enum {
		F_STORE_READONLY = 0x1,
		F_CLEAR = 0x2,
		// The caller publishes the freshness time after all replies.
		F_DEFER_MTIME = 0x4
	};

	virtual void clear(void) = 0;
	virtual int is_empty(void) = 0;

	virtual void add(GroupInfo & gi) = 0;
	virtual void set(GroupInfo & gi, int flags = 0) = 0;

	/* get(group,gi)
	 * Returns information of the newsgroup >group<.
	 * The space for gi must be allocated by the caller.
	 */
	virtual int get(const char *group, GroupInfo * gi) = 0;
	virtual int hasgroup(const char *group) = 0;

	// since: when the list was asked for (0: now), see keep_newer;
	// filter NULL keeps every line
	virtual void read(std::istream & is, const ActiveFilter *filter,
			  int flags = 0, unsigned int limit = 0,
			  nvtime_t since = 0) = 0;
	virtual void write(std::ostream & os, nvtime_t ctime =
			   0, int mode = m_active, const char *filter =
			   NULL) = 0;

	// 0 means no refresh has been recorded; FIRST_REFRESH_PENDING marks an
	// incomplete initial fill and INVALIDATED forces an established list to
	// be refreshed. None is a usable list freshness time.
	virtual void setmtime(unsigned long tm, int force = 0) = 0;
	virtual void getmtime(unsigned long *tm) = 0;

	virtual int lock(int command, int block = NVcontainer::Block) = 0;

	/* begin(), end()
	 * The iterator takes no lock and follows offsets in this process's
	 * mapping of the file: walk it only under a lock (ActiveDBLock),
	 * or a session that adds groups meanwhile grows the file, and the
	 * walk takes the sound file for damaged.
	 */
	virtual Iter < GroupInfo > begin() = 0;
	virtual Iter < GroupInfo > end() = 0;
};

/* ActiveDBLock
 * Holds a lock on the active database for the lifetime of the object.
 */
class ActiveDBLock {
	ActiveDB *_db;
	ActiveDBLock(const ActiveDBLock &);
	ActiveDBLock & operator=(const ActiveDBLock &);
  public:
	ActiveDBLock(ActiveDB * db, int how = NVcontainer::ShrdLock)
	:_db(db) {
		_db->lock(how);
	}
	~ActiveDBLock() {
		try {
			_db->lock(NVcontainer::UnLock);
		}
		catch(...) {
			// (a destructor must not throw; corrupt() may have
			// taken the lock stack down already)
		}
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
