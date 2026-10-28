#ifndef _NVActiveDB_h_
#define _NVActiveDB_h_

#include <stdio.h>
#include <stdlib.h>
#include <errno.h>
#include <sys/types.h>
#include <sys/stat.h>
#include <ctype.h>

#include <iostream>
#include <NVHash.h>

#include "config.h"
#include "Debug.h"
#include "NSError.h"
#include "MPList.h"
#include "ActiveDB.h"

/* System Dependent Data
 * Hashsize of the active database. Use number of groups/(3..10)
 * _Should_ be a prime
 * Eg 211, 503, 1009, 1709, 2903, 4099, 4801, 6007, 7901, 9001, 15013
 */
#define CONF_NVActiveDB_HASHSIZE 15013

#ifdef CONF_NVActiveDB_HASHSIZE
#define NVActiveDB_HASHSIZE CONF_NVActiveDB_HASHSIZE
#else
#define NVActiveDB_HASHSIZE 9001
#endif

class NVActiveDB_Iter;

/**
 * \class NVActiveDB
 * \author Thomas Gschwind
 *
 * \bug Documentation is missing.
 */
class NVActiveDB:public ActiveDB, protected NVHash {
  protected:
	friend class NVActiveDB_Iter;

	unsigned long hash(const char *strg);

	/**
	 * Set newsgroup information for group gi.name(). If the flag 
	 * update_only is specified, it is regarded as error, if gi.name()
	 * does not already exist. Otherwise, it is added to the active
	 * database.
	 * \param gi Description of the newsgroup.
	 * \param flags See description above.
	 */
	void sset(GroupInfo & gi, int flags = 0);

	// The group name in record r of a hash chain, checked: the record
	// comes from the file, and the name must end within it.
	const char *recname(Record * r);

  public:
	NVActiveDB():NVHash()
	{ }

	NVActiveDB(const char *dbname)
		: NVHash(dbname, NVActiveDB_HASHSIZE)
	{ }

	/**
	 * Converts the active database of NewsCache 1.3.21 and older
	 * (32-bit article numbers) in oldname to newname and renames
	 * oldname to oldname.v1. Does nothing if newname exists or
	 * oldname doesn't. Each group keeps its creation time, which
	 * NEWGROUPS reports.
	 * \return the number of groups converted, or -1 on an error (the
	 * 	new database then starts empty and is filled from upstream)
	 */
	static int convert_v1(const char *oldname, const char *newname);

	int lock(int command, int block = Block) {
		return NVHash::lock(command, block);
	}
	int get_lock(void) {
		return NVHash::get_lock();
	}

	void open(const char *dbname) {
		NVHash::open(dbname, NVActiveDB_HASHSIZE);
	}
	int is_open(void) {
		return NVHash::is_open();
	}
	void close(void) {
		NVHash::close();
	}

	void setmtime(unsigned long tm, int force = 0) {
		NVHash::setmtime(tm, force);
	}
	void getmtime(unsigned long *tm) {
		NVHash::getmtime(tm);
	}

	void clear(void) {
		NVHash::clear();
	}
	int is_empty(void) {
		return NVHash::is_empty();
	}

	/**
	 * Adds the description of the newsgroup. If the newsgroup already 
	 * exists in the active database, it adds a duplicate.
	 * \param gi Description of the newsgroup
	 */
	void add(GroupInfo & gi);
	void set(GroupInfo & gi, int flags = 0) {
		lock(ExclLock);
		sset(gi, flags);
		lock(UnLock);
	}

	/**
	 * Stores informations of the newsgroup group in gi.
	 * \return 0 on success and -1 if the requested group cannot be found
	 */
	int get(const char *group, GroupInfo * gi);

	/**
	 * Checks whether the group exists in the active database.
	 * \param group Name of the newsgroup
	 * \return 1 if the group exists, otherwise 0
	 * \throw System System Function Errors
	 * \throw ResponseErr Response Error
	 */
	int hasgroup(const char *group);

	/**
	 * Read active database records and store those matching the filter
	 * expression in the active database. A nonzero limit keeps only the
	 * newest article numbers in each stored range.
	 * \param limit Maximum range size, or 0 for no limit.
	 * \throw System System Function Errors
	 * \throw ResponseErr Response Error
	 */
	void read(std::istream & is, const char *filter, int flags = 0,
		  unsigned int limit = 0);
	void write(std::ostream & os, nvtime_t ctime = 0, int mode =
		   m_active, const char *filter = NULL);

	Iter < GroupInfo > begin();

	Iter < GroupInfo > end();

	friend std::ostream & operator<<(std::ostream & os,
									 NVActiveDB & adb) {
		adb.write(os);
		return os;
	}
};

/****************************************************************/

class NVActiveDB_Iter:public _Iter < GroupInfo > {
  private:
	friend class NVActiveDB;

	NVActiveDB *active_database;

	unsigned long hash_val;
	NVActiveDB::Record * pos;
	NVActiveDB::Record * tail;
	unsigned long steps;	// along the current chain

	GroupInfo newsgroup;

	// skip null entries
	void _skip_nulls() {
		while (hash_val < active_database->hashsz) {
			if (active_database->hashtab[hash_val]) {
				tail =
				    active_database->o2r(active_database->
							 hashtab
							 [hash_val]);
				pos = active_database->o2r(tail->next);
				steps = 0;
				break;
			}
			hash_val++;
		}
	}

      NVActiveDB_Iter(NVActiveDB * db, bool begin):active_database(db)
	{
		_type = 1;

		pos = NULL;
		steps = 0;
		hash_val = begin ? 0 : active_database->hashsz;
		_skip_nulls();
	}

  public:
	_Iter < GroupInfo > *clone() {
		return new NVActiveDB_Iter(*this);
	}

	GroupInfo *get() {
		char *data;
		int datasz;

		data = active_database->mem_p +
		    active_database->r2o(pos) + sizeof(NVActiveDB::Record);
		datasz = pos->szdata;

		newsgroup.setraw(data, datasz);
		return &newsgroup;
	}

	void next() {
		ASSERT(if (hash_val == active_database->hashsz) {
		       slog.p(Logger::Critical) << "iterator past end\n";
		       return;}
		);

		if (pos == tail) {
			pos = NULL;
			hash_val++;
			_skip_nulls();
		} else {
			active_database->chain_step(&steps);
			pos = active_database->o2r(pos->next);
		}
	}

	bool equals(_Iter < GroupInfo > *iter) {
		NVActiveDB_Iter *active_iter = (NVActiveDB_Iter *) iter;

		return active_iter->_type == 1 &&
		    active_iter->active_database == this->active_database
		    && active_iter->pos == this->pos;
	}
};

/****************************************************************/

inline Iter < GroupInfo > NVActiveDB::begin()
{
	return Iter < GroupInfo > (new NVActiveDB_Iter(this, true));
}

inline Iter < GroupInfo > NVActiveDB::end()
{
	return Iter < GroupInfo > (new NVActiveDB_Iter(this, false));
}

#endif

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
