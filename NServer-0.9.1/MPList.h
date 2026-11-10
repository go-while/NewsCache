#ifndef _MPList_h_
#define _MPList_h_
#include <ctype.h>
#include <stdio.h>
#include <netdb.h>
#include <sys/param.h>
#include <sys/socket.h>
#include <sys/types.h>

#include <iostream>
#include <string>
#include <vector>

#include "Debug.h"
#include "Error.h"
#include "OverviewFmt.h"
#include "Lexer.h"

struct MPListEntry {
  public:
	enum {
		F_SETPOSTFLAG = 0x01,
		F_CACHED = 0x02,
		F_OFFLINE = 0x04,
		F_SEMIOFFLINE = 0x08,
		F_DONTGENMSGID = 0x10,
		F_SSL = 0x20,
		F_STARTTLS = 0x40,
		F_TLS_NOVERIFY = 0x80,	// don't verify the TLS certificate
		F_COMPRESS = 0x100	// COMPRESS DEFLATE (RFC 8054)
	};
	// nntp commands supported by the server
	enum {
		F_LIST_ACTIVE_WILDMAT = 0x01,
		F_LIST_OVERVIEW_FMT = 0x02,
		F_LISTGROUP = 0x04,
		F_MODE_READER = 0x08,
		F_OVER = 0x10,
		F_XOVER = 0x20,
		F_POST = 0x40
	};

	std::string hostname;
	std::string servicename;
	std::string user;
	std::string passwd;
	std::string tlsCAFile;	// CA certificates (PEM) to verify TLS
				// against; empty = the system trust store

	OverviewFmt overview;

	std::string read;	// Groups read from that server
	std::string postTo;	// Postings handled by this server
	struct sockaddr_storage bindFrom;	// From which interface

	unsigned int groupTimeout;
	unsigned int flags;	// configuration flags
	unsigned int nntpflags;	// supported NNTP-Commands
	unsigned int limitgroupsize; // limit the size of a group
	unsigned int retries;
	unsigned int connectBackoff;
	unsigned int connectTimeout;
	unsigned int readTimeout;	// per read/write on the connection, 0 = none

	mutable time_t connectFailed;

	MPListEntry();
	void init(void);
	void printParameters (std::ostream *pOut);
};

/**
 * \class MPList
 * \author Thomas Gschwind
 *
 * \bug Documentation is missing.
 */
class MPList {
  public:
	std::vector < MPListEntry > entries;

  private:

  public:
	MPList() {};
//   void addserver(const char *ns,const char *p,const char *g) {//     if(e_used==e_alloc) myrealloc(e_used+1);//     entries[e_used].init(ns,p);//     if(g) {//       strcpy(entries[e_used].read,g);//       strcpy(entries[e_used].postTo,g);//     }//     e_used++;//   }
	// the index of the entry whose Read matches group best (the longest
	// match; the first entry on a tie), -1 for none: the entry server()
	// picks, also a NoServer entry. group may be followed by a space and
	// more, as in an active list line.
	int route(const char *group) const;
	MPListEntry *server(const char *group);
	MPListEntry *postserver(const char *group);
	void init(void);
	void read(Lexer & lex);
	void readServer(Lexer & lex, const char *host, const char *port);
	void printParameters(std::ostream *pOut);

};


// Inline methods
inline MPListEntry::MPListEntry () {
	init();
}

inline void MPListEntry::init(void)
{
	bindFrom.ss_family = AF_UNSPEC;
	groupTimeout = 600;	// 10m
	limitgroupsize = 0;
	retries = 3;

	connectBackoff = 0;
	// 0 left it to the system: a news server that doesn't answer (its
	// SYNs dropped) held each command about two minutes per address,
	// with no reply to the client, and the next command waited again
	connectTimeout = 30;
	readTimeout = 300;
	connectFailed = 0;

	flags = F_SETPOSTFLAG | F_CACHED;
	nntpflags = 0xffffffff;
}

inline void MPList::init(void)
{
	entries.clear();
}

#endif

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
