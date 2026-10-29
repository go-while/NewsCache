#ifndef _NSutil_h_
#define _NSutil_h_

#include <string>
#include <vector>

extern int mkpdir(const char *dirname, int mode);
/* The spool directory of a group (dots become slashes), in a static
 * buffer; NULL if the name doesn't fit. */
extern const char *group2dir(const char *name);

/* The group names in the body of a Newsgroups header: separated by
 * commas, with optional spaces and tabs around them (RFC 5536); empty
 * entries are skipped. Returns false, with an empty list, when a name
 * contains white space or a control character: such a header has no
 * single reading (one parser sees one group where another sees two), so
 * nothing may act on it. Every check and every action on a Newsgroups
 * header goes through this one routine, so that they all see the same
 * groups. */
extern bool split_newsgroups(const std::string & ngs,
			     std::vector < std::string > &groups);

/* NNTP's opaque RFC 3977 Message-ID, at most 250 octets. */
extern bool valid_msgid(const char *id);
/* The stricter RFC 5536 Message-ID header form, at most 250 octets. */
extern bool valid_msgid_5536(const char *id);

/**
 * \author Thomas Gschwind
 * Checks whether a newsgroup matches a given list of patterns. 
 * The pattern-list is a comma separated list of newsgroup-patterns 
 * that may contain a wildcard (*) at the end and start with a !
 * to indicate that the newsgroup must not match a pattern.
 * Eg: comp.*,!comp.os.win* matches all newsgroups starting
 * with comp., except those starting with comp.os.win. This
 * rule is especially designed for our special windows-friends ;)
 *
 * \param pattern The pattern-list.
 * \param group The name of the newsgroup terminated by \\0 or a
 * 	whitespace.
 * \return 
 * 	\li If the newsgroup is matched by a positive pattern, the number of
 * 		matching characters plus one will be returned.
 * 	\li If the newsgroup is matched by a negative pattern (one that
 * 		starts with !), minus the number of characters minus one
 * 		will be returned.
 */
extern int matchgroup(const char *pattern, const char *group);

/**
 * \author Thomas Gschwind
 * Get the fqdn of the local host. The primary host name may be
 * required for expansion of spool_directory and log_file_path, so
 * make sure it is set asap. It is obtained from uname(), but if
 * that yields an unqualified value, make a FQDN by using
 * gethostbyname to canonize it. Some people like upper case letters
 * in their host names, so we don't force the case.
 *
 * \return The FQDN
 */
/* Room for a fully qualified domain name, which DNS allows up to 253
 * characters long. MAXHOSTNAMELEN (64) is only the limit for the kernel
 * host name, not for the name the resolver returns. */
#define FQDN_BUFSIZE 256

/* The host's fully qualified domain name, truncated to FQDN_BUFSIZE - 1
 * characters, or NULL if uname() fails. */
extern const char *getfqdn(void);

#endif

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
