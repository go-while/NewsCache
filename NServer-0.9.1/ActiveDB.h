#ifndef _ActiveDB_h_
#define _ActiveDB_h_

#include <ctype.h>

#include <iostream>

#include "NVcontainer.h"
#include "NSError.h"
#include "GroupInfo.h"

#include "VirtualIterator.h"

class ActiveDB {
  protected:
	ActiveDB();

  public:
	virtual ~ ActiveDB();
	// Used for set
	enum {
		update_only = 0x01
	};

	enum {
		m_active, m_times
	};

	enum {
		F_STORE_READONLY = 0x1,
		F_CLEAR = 0x2
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

	virtual void read(std::istream & is, const char *filter,
			  int flags = 0, unsigned int limit = 0) = 0;
	virtual void write(std::ostream & os, nvtime_t ctime =
			   0, int mode = m_active, const char *filter =
			   NULL) = 0;

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
