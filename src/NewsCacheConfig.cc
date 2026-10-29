#include <ctype.h>
#include <errno.h>
#include <limits.h>
#include <stdio.h>
#include <string.h>
#include <stdlib.h>

#include <iostream>
#include <fstream>

#include "config.h"
#include "Debug.h"
#include "Lexer.h"
#include "NewsCacheConfig.h"
#include "NServer.h"

using namespace std;

/****************************************************************
 * CONFIG
 ****************************************************************/
#define STRNCPY(dest,source) \
    if(source.length()>=sizeof(dest))\
      throw SyntaxError(lex,"argument too long", ERROR_LOCATION);\
    strcpy(dest,source.c_str());

#define HANDLESTRGARG(name) \
    if(tok==#name) {\
      a1=lex.getToken();\
      STRNCPY(name,a1);\
    }

#define HANDLEINTARG(name,min,max) \
    if(tok==#name) {\
      name=getNumber(lex,#name,min,max);\
    }


Config::Config()
{
	init();
}

Config::Config(const char *fn)
{
	init();
	read(fn);
}

void Config::init(void)
{

	Username[0] = Groupname[0] = '\0';
	strcpy(Admin, "notset");

	ServerType = standalone;
	// the default is looked up at the end of read(), and only if the
	// file sets no Hostname: getfqdn() may do a DNS lookup
	Hostname[0] = '\0';
	CachePort[0] = '\0';
	strcpy (ListenTo, "DEFAULT");
	ListenToSet = 0;
	TLSListenTo[0] = TLSCertFile[0] = TLSKeyFile[0] = TLSPriority[0] = '\0';

	LogDirectory[0] = '\0';
	LogStyle = LogName + LogAddr;
	strcpy(SpoolDirectory, "/var/cache/newscache");
	SpoolSize = 1024 * 1024;
	PrefetchFile[0] = PidFile[0] = '\0';

	ClientTimeout = 1200;
	AuthFailureDelay = 1;
	HistoryEnabled = 0;
	MaxConnections = 32;
	MaxArticleSize = 1024 * 1024;
	MaxPostSize = 1024 * 1024;

	NiceServer = 0;
	NiceClient = 10;
	NiceClean = 3;

	ttl_list = 3600;	/* 1hr */
	ttl_desc = 3600;	/* 1hr */

	clnts.init();
	srvrs.init();

}

MPListEntry *Config::server(const char *group)
{
	return srvrs.server(group);
}

AccessEntry *Config::client(const char *name, const struct sockaddr *addr,
			    socklen_t addrlen)
{
	return clnts.client(name, addr, addrlen);
}

void Config::read(const char *fn)
{
	Lexer lex(fn);
	string tok, a1, a2, a3;

	for (;;) {
		tok = lex.getToken();
		if (lex.eof())
			break;

		if (tok == "SpoolDirectory") {
			a1 = lex.getToken();
			if (a1.length() > MAXSPOOLDIRLEN) {
				char msg[128];
				snprintf(msg, sizeof(msg), "SpoolDirectory too long "
					 "(at most %d characters)", (int) MAXSPOOLDIRLEN);
				throw SyntaxError(lex, msg, ERROR_LOCATION);
			}
			STRNCPY(SpoolDirectory, a1);
		} else
			HANDLESTRGARG(LogDirectory)
			    else
			HANDLESTRGARG(PrefetchFile)
			    else
			HANDLESTRGARG(Username)
			    else
			HANDLESTRGARG(Groupname)
			    else
			HANDLESTRGARG(CachePort)
			    else
		if (tok == "ListenTo") {
			a1 = lex.getToken();
			STRNCPY(ListenTo, a1);
			ListenToSet = 1;
		} else
			HANDLESTRGARG(TLSListenTo)
			    else
			HANDLESTRGARG(TLSCertFile)
			    else
			HANDLESTRGARG(TLSKeyFile)
			    else
			HANDLESTRGARG(TLSPriority)
			    else
			HANDLESTRGARG(Admin)
			    else
			HANDLESTRGARG(PidFile)
			    else
			HANDLESTRGARG(Hostname)
			    else
		if (tok == "SpoolSize") {
			char *e;
			long long v;

			// KBytes; newscacheclean works in 512-byte blocks and
			// st_blocks, so no int (it wrapped from 1 TByte on)
			a1 = lex.getToken();
			errno = 0;
			v = strtoll(a1.c_str(), &e, 10);
			if (!isdigit((unsigned char) a1[0]) || *e || errno
			    || v > LLONG_MAX / 1024)
				throw SyntaxError(lex,
						  "SpoolSize expects a size in KBytes",
						  ERROR_LOCATION);
			SpoolSize = v;
		} else
			HANDLEINTARG(ClientTimeout, 0, 86400)
			    else
			HANDLEINTARG(MaxConnections, 1, 1000000)
			    else
			HANDLEINTARG(NiceServer, -20, 20)
			    else
			HANDLEINTARG(NiceClient, -20, 20)
			    else
			HANDLEINTARG(NiceClean, -20, 20)
			    else
		if (tok == "LogStyle") {
			LogStyle = 0;
			for (;;) {
				a1 = lex.getToken();
				if (a1 == "strict-inn")
					LogStyle |= LogINN;
				else if (a1 == "hostname")
					LogStyle |= LogName;
				else if (a1 == "ip-address")
					LogStyle |= LogAddr;
				else
					break;
			}
			lex.putbackToken(a1);
		} else if (tok == "ServerType") {
			a1 = lex.getToken();
			if (a1 == "inetd")
				ServerType = inetd;
			else if (a1 == "standalone")
				ServerType = standalone;
			else
				throw SyntaxError(lex,
						  "illegal ServerType",
						  ERROR_LOCATION);
		} else if (tok == "ConfigVersion") {
			a1 = lex.getToken();
			if (atoi(a1.c_str()) != 5)
				throw SyntaxError(lex,
						  "expecting ConfigVersion 5",
						  ERROR_LOCATION);
		} else if (tok == "NewsServerList") {
			srvrs.read(lex);
		} else if (tok == "AccessList") {
			clnts.read(lex);
		} else if (tok == "AuthFailureDelay") {
			char *e;
			long v;

			a1 = lex.getToken();
			errno = 0;
			v = strtol(a1.c_str(), &e, 10);
			if (!isdigit((unsigned char) a1[0]) || *e || errno
			    || v > 3600)
				throw SyntaxError(lex,
						  "AuthFailureDelay expects seconds, 0 to 3600",
						  ERROR_LOCATION);
			// at most 1 s: a longer (and, before, growing) sleep
			// only kept a process and its memory busy per guessing
			// connection; values up to 3600 are still accepted
			AuthFailureDelay = v > 1 ? 1 : (int) v;
		} else if (tok == "MaxArticleSize" || tok == "MaxPostSize") {
			char *e;
			long v;

			// bytes; an article is held in memory as a whole
			a1 = lex.getToken();
			errno = 0;
			v = strtol(a1.c_str(), &e, 10);
			if (!isdigit((unsigned char) a1[0]) || *e || errno
			    || v < 1024 || v > 1024L * 1024 * 1024) {
				string msg = tok + " expects bytes, 1024 to "
				    "1073741824";
				throw SyntaxError(lex, msg.c_str(),
						  ERROR_LOCATION);
			}
			if (tok == "MaxArticleSize")
				MaxArticleSize = v;
			else
				MaxPostSize = v;
		} else if (tok == "History") {
			a1 = lex.getToken();
			if (a1 == "on" || a1 == "yes")
				HistoryEnabled = 1;
			else if (a1 == "off" || a1 == "no")
				HistoryEnabled = 0;
			else
				throw SyntaxError(lex,
						  "History expects on or off",
						  ERROR_LOCATION);
		} else if (tok == "Timeouts") {
			ttl_list = getNumber(lex, "Timeouts", 0, INT_MAX);
			ttl_desc = getNumber(lex, "Timeouts", 0, INT_MAX);
		} else {
			throw SyntaxError(lex, "unknown keyword",
					  ERROR_LOCATION);
		}
	}

	// TLS for clients: a listener needs the certificate and the key,
	// and the two only make sense together. (They are loaded when the
	// daemon starts, not here: updatenews and newscacheclean read this
	// file too, as the news user, who may not read the key.)
	if ((TLSCertFile[0] != '\0') != (TLSKeyFile[0] != '\0'))
		throw SyntaxError(lex, "TLSCertFile and TLSKeyFile go together",
				  ERROR_LOCATION);
	if (TLSListenTo[0] != '\0') {
#if !defined(HAVE_GNUTLS)
		throw SyntaxError(lex, "TLSListenTo needs a build with GnuTLS",
				  ERROR_LOCATION);
#endif
		if (TLSCertFile[0] == '\0')
			throw SyntaxError(lex, "TLSListenTo needs TLSCertFile "
					  "and TLSKeyFile", ERROR_LOCATION);
	}

	if (Hostname[0] == '\0') {
		const char *fqdn = getfqdn();
		snprintf(Hostname, sizeof(Hostname), "%s",
			 fqdn ? fqdn : "localhost");
	}
}

void Config::printParameters (ostream *pOut)
{
	if (Username[0] != '\0') {
		*pOut << "Username " << Username << endl;
	}
	if (Groupname[0] != '\0') {
		*pOut << "Groupname " << Groupname << endl;
	}
	*pOut << "Admin " << Admin << endl;
	*pOut << "Hostname " << Hostname << endl;
	*pOut << "ServerType ";
	if (ServerType == inetd) {
		*pOut << "inetd" << endl;
	} else if (ServerType == standalone) {
		*pOut << "standalone" << endl;
	} else {
		*pOut << "UNDEFINED!" << endl;
	}
	if (CachePort[0] != '\0') {
		*pOut << "CachePort " << CachePort << endl;
	}
	// (read back, a ListenTo line would make CachePort ignored)
	if (ListenToSet || CachePort[0] == '\0')
		*pOut << "ListenTo " << ListenTo << endl;
	if (TLSListenTo[0] != '\0')
		*pOut << "TLSListenTo " << TLSListenTo << endl;
	if (TLSCertFile[0] != '\0')
		*pOut << "TLSCertFile " << TLSCertFile << endl;
	if (TLSKeyFile[0] != '\0')
		*pOut << "TLSKeyFile " << TLSKeyFile << endl;
	if (TLSPriority[0] != '\0')
		*pOut << "TLSPriority " << TLSPriority << endl;
	if (LogDirectory[0] != '\0') {
		*pOut << "LogDirectory " << LogDirectory << endl;
	}
	*pOut << "SpoolDirectory " << SpoolDirectory << endl;
	*pOut << "LogStyle";
	if (LogStyle & LogINN) {
		*pOut << " strict-inn";
	}
	if (LogStyle & LogName) {
		*pOut << " hostname";
	}
	if (LogStyle & LogAddr) {
		*pOut << " ip-address";
	}
	*pOut << endl;
	*pOut << "SpoolSize " << SpoolSize << endl;
	if (PrefetchFile[0] != '\0') {
		*pOut << "PrefetchFile " << PrefetchFile << endl;
	}
	if (PidFile[0] != '\0') {
		*pOut << "PidFile " << PidFile << endl;
	}
	*pOut << "ClientTimeout " << ClientTimeout << endl;
	*pOut << "AuthFailureDelay " << AuthFailureDelay << endl;
	*pOut << "History " << (HistoryEnabled ? "on" : "off") << endl;
	*pOut << "MaxConnections " << MaxConnections << endl;
	*pOut << "MaxArticleSize " << MaxArticleSize << endl;
	*pOut << "MaxPostSize " << MaxPostSize << endl;
	*pOut << "Timeouts " << ttl_list << " " << ttl_desc << endl;
	*pOut << "NiceServer " << NiceServer << endl;
	*pOut << "NiceClient " << NiceClient << endl;
	*pOut << "NiceClean " << NiceClean << endl;

	clnts.printParameters (pOut);
	srvrs.printParameters (pOut);
}

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
