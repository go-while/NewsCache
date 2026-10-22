#include"NVcontainer.h"
#include"Error.h"

#include <iostream>
#include<fcntl.h>
#include<stdio.h>
#include<sys/mman.h>
#include<sys/stat.h>
#include<sys/types.h>
#include<time.h>

#ifdef HAVE_SSTREAM
#include <sstream>
#else
#include <strstream>
#endif

using namespace std;

int nvflock(int fd, int cmnd, int block)
{
#ifdef USE_FLOCK
	int ncmnd;

	switch (cmnd) {
	case NVcontainer::UnLock:
		ncmnd = LOCK_UN;
		break;
	case NVcontainer::ShrdLock:
		ncmnd = LOCK_SH;
		break;
	case NVcontainer::ExclLock:
		ncmnd = LOCK_EX;
		break;
	default:
		DEBUG(cdbg << "myflockerr\n");
		return EINVAL;
	}
	if (block == NVList::NoBlock)
		ncmnd = ncmnd | LOCK_NB;

	return flock(fd, ncmnd);
#else
	int lck;
	struct flock ncmnd;
	int r;

	switch (cmnd) {
	case NVcontainer::UnLock:
		ncmnd.l_type = F_UNLCK;
		break;
	case NVcontainer::ShrdLock:
		ncmnd.l_type = F_RDLCK;
		break;
	case NVcontainer::ExclLock:
		ncmnd.l_type = F_WRLCK;
		break;
	default:
		ASSERT(cerr << "myflockerr\n");
		return EINVAL;
	}
	ncmnd.l_whence = SEEK_SET;
	ncmnd.l_start = 0;
	ncmnd.l_len = 0;
	if (block == NVcontainer::NoBlock)
		lck = F_SETLK;
	else
		lck = F_SETLKW;

	// Unlock, before applying the new lock
	if (ncmnd.l_type != F_UNLCK) {
		struct flock ulck;
		ulck.l_type = F_UNLCK;
		ulck.l_whence = SEEK_SET;
		ulck.l_start = 0;
		ulck.l_len = 0;
		if (fcntl(fd, lck, &ulck) < 0)
			return -1;
	}
	if ((r = fcntl(fd, lck, &ncmnd)) < 0) {
		if (errno == EACCES || errno == EAGAIN)
			errno = EWOULDBLOCK;
		return -1;
	}
	return 0;
#endif
}

nvtime_t nvtime(nvtime_t * nvt)
{
	time_t tm;
	nvtime_t nvtm;

	time(&tm);
	nvtm = tm;
	if (nvt)
		*nvt = nvtm;

	return nvtm;
}

NVcontainer::NVcontainer()
{
	lck_stackp = NVcontainer_LOCKSTACKSIZE;
	lck_orphans = in_open = in_corrupt = 0;
	mem_fn[0] = '\0';
	mem_fd = -1;
	mem_p = NULL;
	mem_sz = 0;
	mem_hdr = (Header *) mem_p;
}

NVcontainer::NVcontainer(const char *dbname, int flags)
{
	lck_stackp = NVcontainer_LOCKSTACKSIZE;
	lck_orphans = in_open = in_corrupt = 0;
	mem_fn[0] = '\0';
	mem_fd = -1;
	mem_p = NULL;
	mem_sz = 0;
	mem_hdr = (Header *) mem_p;
	open(dbname, flags);
}

NVcontainer::~NVcontainer()
{
	close();
}

void NVcontainer::make_current()
{
	struct stat st;

	if (mem_p) {
		if (is_current())
			return;
		munmap(mem_p, mem_sz);
		mem_p = NULL;
	}

	if (fstat(mem_fd, &st) < 0) {
		throw SystemError("NVContainer(20801): fstat failed",
				  errno, ERROR_LOCATION);
	}

	if ((nvoff_t) st.st_size <=
	    (nvoff_t) sizeof(Header) + (nvoff_t) sizeof(FreeList)) {
#ifdef HAVE_SSTREAM
		stringstream sb;
#else
		strstream sb;
#endif
		sb << "file shorter than its header ("
		   << (unsigned long int) st.st_size << " bytes)";
		corrupt(sb.str());
		return;
	}

	mem_sz = st.st_size;
	mem_p = (char *) mmap(NULL, mem_sz,
			      PROT_READ | PROT_WRITE, MAP_SHARED,
			      mem_fd, 0);
	if (mem_p == (char *) -1) {
		mem_p = NULL;
		throw SystemError("NVContainer::make_current(): mmap failed", errno,
				  ERROR_LOCATION);
	}
	mem_hdr = (Header *) mem_p;
	//Do some sanity checks on the database
	if (mem_hdr->hlen != sizeof(Header)) {
#ifdef HAVE_SSTREAM
		stringstream sb;
#else
		strstream sb;
#endif
		sb << "wrong header size " << (unsigned long int) mem_hdr->hlen
		   << ", not " << (unsigned long int) sizeof(Header);
		corrupt(sb.str());
		return;
	}
	if (mem_hdr->version != 2) {
#ifdef HAVE_SSTREAM
		stringstream sb;
#else
		strstream sb;
#endif
		sb << "wrong header version " << (unsigned long int) mem_hdr->version;
		corrupt(sb.str());
		return;
	}
	// The file must be as long as the header says (a shorter one is
	// cut off: accessing the rest would end with SIGBUS), and the free
	// list and the user data must lie within it.
	if (mem_hdr->size > mem_sz) {
#ifdef HAVE_SSTREAM
		stringstream sb;
#else
		strstream sb;
#endif
		sb << "file cut off: " << (unsigned long int) mem_sz
		   << " bytes, the header says " << (unsigned long int) mem_hdr->size;
		corrupt(sb.str());
		return;
	}
	{
		nvoff_t fl = mem_hdr->freelist;
		if (fl < sizeof(Header) || fl > mem_sz - sizeof(FreeList) ||
		    fl % sizeof(unsigned long) ||
		    ((FreeList *) (mem_p + fl))->size > mem_sz - fl ||
		    mem_hdr->bytes_free > mem_sz) {
			corrupt("bad free list in the header");
			return;
		}
		if (mem_hdr->userdata && !valid_block(mem_hdr->userdata)) {
			corrupt("bad user data offset in the header");
			return;
		}
	}
	if (mem_sz != mem_hdr->size) {
#ifdef HAVE_SSTREAM
		stringstream sb;
#else
		strstream sb;
#endif
		sb << "NVConatiner::make_current(): wrong database size stored "
		   << "in db-header - may be a hazard"
		   << " mem_fn: " << mem_fn
		   << " mem_sz: " << (unsigned long int) mem_sz
		   << " mem_hdr->size: " << (unsigned long int) mem_hdr->size;
		slog.p(Logger::Critical) << sb.rdbuf()->str();
	}
}

size_t NVcontainer::resize(size_t nsz)
{
	if (nsz < mem_sz) {
		VERB(slog.
		     p(Logger::
		       Warning) <<
		     "NVContainer: reduction of database size not supported, ignored\n");
		return mem_sz;
	}

	lock(ExclLock);
	FreeList *flhead = o2fl(mem_hdr->freelist);

	nsz = (nsz + 0x10000) & (~0xffff);
	if (ftruncate(mem_fd, nsz) < 0) {
#ifdef HAVE_SSTREAM
		stringstream sb;
#else
		strstream sb;
#endif
		sb << "NVConatiner::resize(): ftruncate failed  "
		   << " mem_fn: " << mem_fn
		   << " nsz: " << (unsigned long int) nsz;
		throw SystemError(sb.rdbuf()->str(), errno, ERROR_LOCATION);
	}
	mem_hdr->bytes_free += nsz - mem_sz;
	flhead->size += nsz - mem_sz;
	mem_hdr->size = nsz;
	make_current();
	lock(UnLock);
	return mem_sz;
}

nvoff_t NVcontainer::getdata()
{
	return mem_hdr->userdata;
}

nvoff_t NVcontainer::getdatap()
{
	return ((char *) &mem_hdr->userdata - mem_p);
}

void NVcontainer::setdata(nvoff_t d)
{
	mem_hdr->userdata = d;
}

nvoff_t NVcontainer::nvalloc(size_t rsz)
{
	lock(ExclLock);
	FreeList *flhead = o2fl(mem_hdr->freelist);
	FreeList *cur, *prv, *res;

	// The allocated memory block has to meet several requirements
	// * aligned on 16 Byte boundary
	// * size has to be a multiple of 16 (simplifies the alignment)
	// * the allocated block has to hold its size
	//   the memory after the size indicator will be returned
	rsz = (rsz + sizeof(unsigned long) + 0xf) & (~0xf);
	if (flhead == NULL)
		corrupt("no free list");
	// flhead points to the last free memory block available in the database
	prv = flhead;
	cur = o2fl(prv->next);
	if (!cur)
		corrupt("free list broken");
	// find the first suitable memory block in the database (the list is
	// sorted by address and ends with flhead)
	while (cur->size < rsz && cur != flhead) {
		prv = cur;
		cur = o2fl(cur->next);
		if (!cur || cur <= prv)
			corrupt("free list out of order");
	}

	if (cur == flhead) {
		// Special care has to be taken for the last free memory block
		// It must always be present! So we have to resize it, if its
		// memory is smaller than the requested size plus the size 
		// necessary to maintain the last block of free memory.
		if (flhead->size < rsz + sizeof(FreeList)) {
			nvoff_t prvoff = fl2o(prv);
			// Since it is very likely that the mapped region will change
			// during the resize operation, we will have to save the
			// currently used pointers
			size_t needsz = rsz + sizeof(FreeList) - flhead->size;
			resize(mem_sz + needsz);
			cur = flhead = o2fl(mem_hdr->freelist);
			prv = o2fl(prvoff);
			if (!cur || !prv || cur->size < rsz + sizeof(FreeList))
				corrupt("free list broken after growing the file");
		}
	}
	if (cur->size < rsz)
		corrupt("free list broken");

	res = cur;

	if (cur->size < rsz + sizeof(FreeList)) {
		// The available space is too small to split?
		// This may only occur if cur _is_not_ the tail of the free blocks
		if (res == flhead)
			corrupt("free block at the end too small");
		prv->next = cur->next;
		rsz = cur->size;
	} else {
		// Split the space into two parts
		cur = (FreeList *) ((char *) cur + rsz);
		cur->size = res->size - rsz;
		if (prv != res) {
			// list contains at least two blocks
			cur->next = res->next;
			prv->next = fl2o(cur);
			if (res == flhead) {
				mem_hdr->freelist = fl2o(cur);
			}
		} else {
			// list contains only one block pointing to itself
			// this must be the free memory block at the tail
			if (res != flhead)
				corrupt("lost the free block at the end");
			mem_hdr->freelist = cur->next = fl2o(cur);
		}
	}
	mem_hdr->bytes_free -= rsz;
	lock(UnLock);

	*(unsigned long *) res = rsz;
#ifdef FREELIST_ASSERT_ON
	char buf[256];
	int i = 10000;
	prv = flhead = o2fl(mem_hdr->freelist);;
	cur = o2fl(prv->next);

	VERB(sprintf
	     (buf, "nvalloc: checking freelist flhead=%p\n", flhead);
	     slog.p(Logger::Debug) << buf);
	while (cur != flhead && i) {
		VERB(sprintf
		     (buf, "nvalloc: elem(%lu)={%lu,%lu}\n",
		      (nvoff_t) ((char *) cur - mem_p), cur->next,
		      cur->size); slog.p(Logger::Debug) << buf);
		prv = cur;
		cur = o2fl(cur->next);
		ASSERT(if (prv >= cur) {
		       throw
		       AssertionError
		       ("NVContainer::nvalloc: freelist out of order!",
			ERROR_LOCATION);}
		);
		i--;
	}
	VERB(sprintf
	     (buf, "nvfree: tail(%lu)={%lu,%lu}\n",
	      (nvoff_t) ((char *) cur - mem_p), cur->next, cur->size);
	     slog.p(Logger::Debug) << buf);
	if (!i)
		VERB(slog.
		     p(Logger::
		       Debug) <<
		     "nvalloc: more than 10000 free blocks!\n");
#endif
	return (nvoff_t) ((char *) res - mem_p) + sizeof(unsigned long);
}

void NVcontainer::nvfree(nvoff_t p)
{
#ifdef FREELIST_ASSERT_ON
	VERB(char buf[256];
	     sprintf(buf, "NVcontainer::nvfree(%lu)\n", p);
	     slog.p(Logger::Debug) << buf);
#endif
	lock(ExclLock);

	FreeList *pb, *pe;
	unsigned long psz;
#ifdef FREELIST_ASSERT_ON
	FreeList *flhead;
	FreeList *cur, *prv;
	int i = 10000;
	prv = flhead = o2fl(mem_hdr->freelist);;
	cur = o2fl(prv->next);

	VERB(sprintf(buf, "NVContainer::nvfree: checking freelist flhead=%p\n", flhead);
	     slog.p(Logger::Debug) << buf);
	while (cur != flhead && i) {
		VERB(sprintf
		     (buf, "NVContainer::nvfree: elem(%lu)={%lu,%lu}\n",
		      (nvoff_t) ((char *) cur - mem_p), cur->next,
		      cur->size); slog.p(Logger::Debug) << buf);
		prv = cur;
		cur = o2fl(cur->next);
		ASSERT(if (prv >= cur) {
		       throw
		       AssertionError
		       ("NVContainer::nvfree: freelist out of order!",
			ERROR_LOCATION);}
		);
		i--;
	}
	VERB(sprintf
	     (buf, "NVContainer::nvfree: tail(%lu)={%lu,%lu}\n",
	      (nvoff_t) ((char *) cur - mem_p), cur->next, cur->size);
	     slog.p(Logger::Debug) << buf);
	if (!i)
		VERB(slog.
		     p(Logger::
		       Debug) << "NVContainer::nvfree: more than 10000 free blocks!\n");
#else
	FreeList *flhead = o2fl(mem_hdr->freelist);
	FreeList *cur = NULL, *prv = NULL;
#endif

	// p comes from the file (an index or a record chain)
	if (!valid_block(p))
		corrupt("freeing a block outside the file");
	pb = (FreeList *) (mem_p + p - sizeof(unsigned long));
	psz = *(unsigned long *) pb;
	pe = (FreeList *) ((char *) pb + psz);
	if (!flhead)
		corrupt("no free list");

	// flhead points to the last free memory block available in the database
	prv = flhead;
	cur = o2fl(prv->next);
	if (!cur)
		corrupt("free list broken");
	while (cur < pb && cur != flhead) {
		prv = cur;
		cur = o2fl(cur->next);
		if (!cur || prv >= cur)
			corrupt("free list out of order");
	}
	if (cur < pb || pe > cur ||
	    (prv < pb && (char *) prv + prv->size > (char *) pb))
		corrupt("freeing a block that overlaps free memory");

	if ((char *) prv + prv->size == (char *) pb) {
#ifdef FREELIST_ASSERT_ON
		VERB(slog.
		     p(Logger::Debug) << "NVContainer::nvfree: adjacent to previous\n");
#endif
		// The freed block is adjacent to the previous free block
		prv->size += psz;
		if (pe == cur) {
#ifdef FREELIST_ASSERT_ON
			VERB(slog.
			     p(Logger::
			       Debug) <<
			     "NVContainer::nvfree: adjacent to next---cool\n");
#endif
			// The freed block is adjacent to the next free block too, cool :)
			if (cur == flhead)
				mem_hdr->freelist = fl2o(prv);
			prv->size += cur->size;
			prv->next = cur->next;
		}
	} else if (pe == cur) {
#ifdef FREELIST_ASSERT_ON
		VERB(slog.
		     p(Logger::Debug) << "NVContainer::nvfree: adjacent to next\n");
#endif
		// The freed block is adjacent to the next free block
		if (cur->next == fl2o(cur)) {
			// Only one block of free memory left
			if (flhead != cur)
				corrupt("lost the free block at the end");
			mem_hdr->freelist = pb->next = fl2o(pb);
			pb->size = psz + cur->size;
		} else {
			pb->next = cur->next;
			pb->size = psz + cur->size;
			prv->next = fl2o(pb);
			if (cur == flhead)
				mem_hdr->freelist = fl2o(pb);
		}
	} else {
		VERB(slog.p(Logger::Debug) << "NVContainer::nvfree: not adjacent\n");
		// The freed block neither is adjacent to the previous nor to the 
		// next free block
		pb->next = fl2o(cur);
		pb->size = psz;
		prv->next = fl2o(pb);
	}
	mem_hdr->bytes_free += psz;
#ifdef FREELIST_ASSERT_ON
	i = 10000;
	prv = flhead = o2fl(mem_hdr->freelist);;
	cur = o2fl(prv->next);

	VERB(sprintf(buf, "NVContainer::nvfree: checking freelist flhead=%p\n", flhead);
	     slog.p(Logger::Debug) << buf);
	while (cur != flhead && i) {
		VERB(sprintf
		     (buf, "NVContainer::nvfree: elem(%lu)={%lu,%lu}\n",
		      (nvoff_t) ((char *) cur - mem_p), cur->next,
		      cur->size); slog.p(Logger::Debug) << buf);
		prv = cur;
		cur = o2fl(cur->next);
		ASSERT(if (prv >= cur) {
		       throw
		       AssertionError
		       ("NVContainer::nvfree: freelist out of order!",
			ERROR_LOCATION);}
		);
		i--;
	}
	VERB(sprintf
	     (buf, "NVContainer::nvfree: tail(%lu)={%lu,%lu}\n",
	      (nvoff_t) ((char *) cur - mem_p), cur->next, cur->size);
	     slog.p(Logger::Debug) << buf);
	if (!i)
		VERB(slog.
		     p(Logger::
		       Debug) << "NVContainer::nvfree: more than 10000 free blocks!\n");
#endif
	lock(UnLock);
}

int NVcontainer::lock(int command, int block)
{
	int res;
	int curLock = UnLock;

	if (lck_stackp < NVcontainer_LOCKSTACKSIZE) {
		curLock = lck_stack[lck_stackp];
	}
	if (command == UnLock) {
		// Unlock container 
		if (lck_stackp >= NVcontainer_LOCKSTACKSIZE && lck_orphans > 0) {
			// a lock that corrupt() gave up
			lck_orphans--;
			return 0;
		}
		if (lck_stackp >= NVcontainer_LOCKSTACKSIZE) {
			throw
			    Error
			    ("NVContainer::lock(): Lock Stack underflow",
			     ERROR_LOCATION);
		}
		lck_stackp++;
		command =
		    lck_stackp <
		    NVcontainer_LOCKSTACKSIZE ? lck_stack[lck_stackp] :
		    UnLock;
	} else {
		// Lock container
		if (lck_stackp == 0) {
			throw
			    Error
			    ("NVContainer::lock(): Lock Stack overflow",
			     ERROR_LOCATION);
		}
		// Is already locked with excl => new lock must be excl
		if (curLock == ExclLock)
			command = ExclLock;
		lck_stack[--lck_stackp] = command;
	}
	// Relock if lock state changed
	if (curLock != command) {
		if ((res = nvflock(mem_fd, command, block)) < 0) {
			if (errno == EWOULDBLOCK && block == NoBlock) {
				lck_stackp++;
				return -1;
			}
			throw
			    SystemError("NVContainer::lock(): flock failed",
					errno, ERROR_LOCATION);
		}
		// Adapt to the new size if somebody changed the container's 
		// size
		if (command != UnLock)
			make_current();
	}
	return 0;
}

int NVcontainer::get_lock()
{
	return lck_stackp <
	    NVcontainer_LOCKSTACKSIZE ? lck_stack[lck_stackp] : UnLock;
}

/*
 */
void NVcontainer::open(const char *dbname, int flags)
{
	ASSERT(if (!dbname) throw
	       AssertionError
	       ("NVContainer::open() called with null-pointer\n",
		ERROR_LOCATION));

	if (strcmp(mem_fn, dbname) == 0)
		return;

	if (mem_fd >= 0)
		close();

	struct stat st;

	flags = 0;
	strcpy(mem_fn, dbname);
	if ((mem_fd =::open(mem_fn, O_RDWR | O_CREAT, 0644)) < 0) {
#ifdef HAVE_SSTREAM
		stringstream sb;
#else
		strstream sb;
#endif
		sb << "NVConatiner::open(): cannot open("
		   << " mem_fn: " << mem_fn
		   << ",O_RDWR|O_CREAT)";
		throw SystemError(sb.rdbuf()->str(), errno, ERROR_LOCATION);
	}

	nvflock(mem_fd, ExclLock, Block);
	if (fstat(mem_fd, &st) < 0) {
#ifdef HAVE_SSTREAM
		stringstream sb;
#else
		strstream sb;
#endif
		sb << "NVConatiner::open(): fstat failed"
		   << " mem_fn: " << mem_fn;
		throw SystemError(sb.rdbuf()->str(), errno, ERROR_LOCATION);
	}
	if (st.st_size == 0) {
		Header hdr;
		FreeList fl;
		hdr.freelist = hdr.hlen;
		hdr.size = 0x10000;
		fl.next = hdr.freelist;
		fl.size = hdr.bytes_free = hdr.size - hdr.hlen;

		if (ftruncate(mem_fd, hdr.size) < 0) {
			unlink(mem_fn);
#ifdef HAVE_SSTREAM
			stringstream sb;
#else
			strstream sb;
#endif
			sb << "NVConatiner::open(): ftruncate failed, unlinked"
			   << " mem_fn: " << mem_fn;
			throw SystemError (sb.rdbuf()->str(), errno, ERROR_LOCATION);
			}
		if (write(mem_fd, &hdr, sizeof(Header)) != sizeof(Header)) {
			unlink(mem_fn);
#ifdef HAVE_SSTREAM
			stringstream sb;
#else
			strstream sb;
#endif
			sb << "NVConatiner::open(): write(info-record) failed, unlinked"
			   << " mem_fn: " << mem_fn;
			throw SystemError (sb.rdbuf()->str(), errno, ERROR_LOCATION);
		}
		if (write(mem_fd, &fl, sizeof(FreeList)) !=
		    sizeof(FreeList)) {
			unlink(mem_fn);
#ifdef HAVE_SSTREAM
			stringstream sb;
#else
			strstream sb;
#endif
			sb << "NVConatiner::open(): write(free-record) failed unlinked"
			   << " mem_fn: " << mem_fn;
			throw SystemError (sb.rdbuf()->str(), errno, ERROR_LOCATION);
		}
	}
	nvflock(mem_fd, UnLock, Block);

	//Sanity checks of an existing database will be done in 
	in_open++;
	try {
		make_current();
	}
	catch(...) {
		in_open--;
		throw;
	}
	in_open--;
}

int NVcontainer::is_open(void)
{
	return mem_fd >= 0;
}

void NVcontainer::close()
{
	if (mem_fd < 0)
		return;
	munmap(mem_p, mem_sz);
	::close(mem_fd);
	if (lck_stackp != NVcontainer_LOCKSTACKSIZE) {
		VERB(slog.
		     p(Logger::
		       Critical) <<
		     "NVcontainer::close: Forgot to unlock the container before closing it!\n");
		lck_stackp = NVcontainer_LOCKSTACKSIZE;
	}
	mem_fn[0] = '\0';
	mem_fd = -1;
	mem_p = NULL;
	mem_hdr = (Header *) mem_p;
}

bool NVcontainer::valid_block(nvoff_t p, size_t * sz)
{
	unsigned long bs;

	if (!mem_p || p < sizeof(Header) + sizeof(unsigned long) ||
	    p > mem_sz || p % sizeof(unsigned long))
		return false;
	// nvalloc stores the block size, its own field included, just
	// before the block
	bs = *(unsigned long *) (mem_p + p - sizeof(unsigned long));
	if (bs < 2 * sizeof(unsigned long) || bs % sizeof(unsigned long) ||
	    bs > mem_sz - (p - sizeof(unsigned long)))
		return false;
	if (sz)
		*sz = bs - sizeof(unsigned long);
	return true;
}

void NVcontainer::corrupt(const std::string & why)
{
	string fn = mem_fn, bad = fn + ".bad";
	int opening = in_open;

	if (in_corrupt || fn.empty()) {
		// the new file is bad too, or there is no file: give up
		throw CorruptDBError(fn, why);
	}
	in_corrupt = 1;
	slog.p(Logger::Error) << "corrupt database " << fn << ": " << why
		<< "; moved to " << bad << ", starting a new one\n";
	struct stat ours, named;
	if (mem_fd >= 0 && fstat(mem_fd, &ours) == 0 &&
	    stat(fn.c_str(), &named) == 0 &&
	    (ours.st_dev != named.st_dev || ours.st_ino != named.st_ino)) {
		// another process has moved it aside already and started
		// a new one: use that
	} else if (::rename(fn.c_str(), bad.c_str()) != 0 && errno != ENOENT) {
		slog.p(Logger::Error) << "cannot rename " << fn << ": "
			<< strerror(errno) << "; removing it\n";
		if (unlink(fn.c_str()) != 0) {
			in_corrupt = 0;
			throw CorruptDBError(fn, why);
		}
	}

	// Drop the old mapping. The callers' locks go with it; their UnLock
	// calls are let through (lck_orphans).
	if (mem_p)
		munmap(mem_p, mem_sz);
	if (mem_fd >= 0)
		::close(mem_fd);
	lck_orphans += NVcontainer_LOCKSTACKSIZE - lck_stackp;
	lck_stackp = NVcontainer_LOCKSTACKSIZE;
	mem_fn[0] = '\0';
	mem_fd = -1;
	mem_p = NULL;
	mem_hdr = NULL;
	mem_sz = 0;
	try {
		open(fn.c_str());
		reinit();
	}
	catch(const Error & e) {
		slog.p(Logger::Error) << "cannot start a new " << fn << "\n";
		e.print();
	}
	in_corrupt = 0;
	if (!opening || mem_fd < 0)
		throw CorruptDBError(fn, why);
}

void NVcontainer::setmtime(nvtime_t tm, int force)
{
	lock(ExclLock);
	unsigned long ctm = mem_hdr->mtime;
	if (force || tm > ctm)
		mem_hdr->mtime = tm;
	lock(UnLock);
}

void NVcontainer::getmtime(nvtime_t * tm)
{
	lock(ShrdLock);
	(*tm) = mem_hdr->mtime;
	lock(UnLock);
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
