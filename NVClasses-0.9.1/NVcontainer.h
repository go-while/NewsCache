#ifndef _NVcontainer_h_
#define _NVcontainer_h_

#include <fcntl.h>
#include <string>
#include <sys/file.h>
#include <sys/param.h>
#include <unistd.h>

#include "Debug.h"

#define NVcontainer_LOCKSTACKSIZE 16

typedef unsigned long nvtime_t;
typedef unsigned long nvoff_t;

extern nvtime_t nvtime(nvtime_t * nvt);

/** 
 * \class NVcontainer
 * \author Thomas Gschwind
 * How should we define a uniform interface to access data
 * stored within an NVcontainer? Two kinds of accesses exist:
 * shared access ... uses NVcontainer's data copy
 * copy access ... copies the data at each request
 * The second version is easier to use, but reduces the performance.
 *
 * \bug Documentation is missing.
 */
class NVcontainer {
	struct Header {
		unsigned int hlen;	/* 32 */
		unsigned int version;	/* 32 */
		nvoff_t size;		/* 32/64 */
		nvtime_t mtime;		/* 32/64 */
		nvoff_t freelist;	/* 32/64 */
		nvoff_t bytes_free;	/* 32/64 */
		nvoff_t userdata;	/* 32/64 */
		 Header() {
			clear();
		} void clear() {
			hlen = sizeof(Header);
			version = 2;
			size = hlen;
			mtime = 0;
			freelist = userdata = 0;
			bytes_free = 0;
	}};
	struct FreeList {
		nvoff_t next;	/* 32/64 */
		nvoff_t size;	/* 32/64 */
	};
	int lck_stack[NVcontainer_LOCKSTACKSIZE];
	int lck_stackp;
	// Locks given up by corrupt(): the matching UnLock calls of the
	// callers find an empty stack and must not fail.
	int lck_orphans;
	int in_open;		// open() is running (see corrupt())
	int in_corrupt;
	// The file corrupt() moved aside but couldn't replace (no file
	// descriptor, no space): lock() tries to open it again.
	char lost_fn[MAXPATHLEN];

	char mem_fn[MAXPATHLEN];
	int mem_fd;
	Header *mem_hdr;

	// The free-list entry at offset o, checked: o comes from the file.
	FreeList *o2fl(nvoff_t o) {
		if (!o)
			return NULL;
		if (o < sizeof(Header) || o > mem_sz - sizeof(FreeList) ||
		    o % sizeof(unsigned long))
			corrupt("free list entry out of range");
		FreeList *f = (FreeList *) (mem_p + o);
		if (f->size > mem_sz - o)
			corrupt("free list entry too big");
		return f;
	}
	nvoff_t fl2o(FreeList * r) {
		return r ? (char *) r - mem_p : 0;
	}

  protected:
	NVcontainer(void);
	NVcontainer(const char *dbname, int flags = 0);
	virtual ~ NVcontainer(void);

	char *mem_p;
	size_t mem_sz;
	// The header's size when the file was mapped (see is_current)
	size_t mem_hdrsz;

	/*
	 * The method make_current map the database file into shared memory.
	 * It does some sanity checks; a file that fails them is handed to
	 * corrupt(). If the database filesize is changed, then it is
	 * necessary to call it again.
	 * \throw Error, SystemError and CorruptDBError
	 */
	virtual void make_current(void);

	/*
	 * Whether the mapping is still current: no process has resized the
	 * file since it was mapped (the header's size is the same). Not the
	 * mapped size: a file longer than its header says (a crash in
	 * resize) was remapped at every lock change.
	 */
	virtual int is_current(void) {
		return mem_hdr && mem_hdr->size == mem_hdrsz;
	}
	virtual size_t resize(size_t nsz);

	nvoff_t getdata();
	nvoff_t getdatap();
	void setdata(nvoff_t d);

	nvoff_t nvalloc(size_t rsz);
	void nvfree(nvoff_t f);

	/*
	 * Whether p is the offset of an allocated block (as nvalloc returns
	 * it) that lies within the file, and its usable size in *sz.
	 * Offsets read from the file must pass this before they are used.
	 */
	bool valid_block(nvoff_t p, size_t * sz = NULL);

	/*
	 * The file failed a sanity check: logs it, moves it to name.bad and
	 * starts a new, empty database under the old name (reinit() then
	 * sets up the derived class's data). While open() runs, it returns
	 * and the caller carries on with the new file; otherwise it throws
	 * CorruptDBError.
	 */
	void corrupt(const std::string & why);
	virtual void reinit(void) {
	}
	/*
	 * corrupt() has unmapped the file: drop every pointer into it, so
	 * nothing uses the old mapping if no new file can be opened.
	 */
	virtual void forget(void) {
	}
  public:
	enum {
		UnLock = F_UNLCK,
		ShrdLock = F_RDLCK,
		ExclLock = F_WRLCK
	};
	enum {
		Block = 0x0,
		NoBlock = 0x1
	};

	int lock(int command, int block = Block);
	int get_lock(void);

	// Not virtual: every caller calls its own class's open(), and
	// NVHash/NVActiveDB have open()s with other parameters.
	void open(const char *dbname, int flags = 0);
	virtual int is_open(void);
	
	/*
	 * Close the file and reset the internal data.
	 */
	virtual void close(void);


	void setmtime(nvtime_t tm, int force = 0);
	void getmtime(nvtime_t * tm);
};

/*
 * A lock of a container (ShrdLock or ExclLock) that is given back when
 * the guard goes out of scope, also when an exception leaves the block:
 * nvalloc throws when the disk is full, and a lock left on the stack kept
 * the flock (with forked sessions: every other client of that database
 * waited) until the session ended. take = false: no lock (for "only if
 * the caller doesn't hold one"). release() gives it back early.
 */
class NVLock {
	NVcontainer & _c;
	bool _held;
	NVLock(const NVLock &);
	NVLock & operator=(const NVLock &);
  public:
	NVLock(NVcontainer & c, int how, bool take = true)
	:_c(c), _held(false) {
		if (take) {
			_c.lock(how);
			_held = true;
		}
	}
	void release(void) {
		if (_held) {
			_held = false;
			_c.lock(NVcontainer::UnLock);
		}
	}
	~NVLock() {
		try {
			release();
		}
		catch(...) {
			// (a destructor must not throw; corrupt() may have
			// taken the lock stack down already)
		}
	}
};

/* We should add a general iterator class */
/*
class NVcontainterIter {
...
};
*/

#endif

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
