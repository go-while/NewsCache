#ifndef __readline_h__
#define __readline_h__

#include<string>

/*
 * The longest line NewsCache reads from a news server outside an
 * article (bytes before the "\n"): status lines (RFC 3977: 512),
 * overview records (long References and Xref make a few KB), lines of
 * the active list and of LISTGROUP. A longer line is an error.
 */
#define NNTP_MAXLINE 16384

/**
 * \author Thomas Gschwind
 * \param is Input-Stream.
 * \param strg String to store line.
 * \param keepnl Keep newline (Default=1).
 * \param max Longest line in bytes (Default=0: no limit). A longer
 * 	line is dropped (strg is empty), the stream gets eofbit and badbit.
 */
int nlreadline(std::istream & is, std::string & strg, int keepnl = 1,
		size_t max = 0);

/**
 * \author Thomas Gschwind
 * \param is Input-Stream.
 * \param strg String to store the text.
 * \param keepnl Keep newlines in text (Default=1).
 * \param eot EndOfText Marker. (Default=".\\r\\n").
 * \param max Largest text in bytes (Default=0: no limit).
 * \return The number of lines; -1 when the text is larger than max
 * 	(the stream gets eofbit and badbit then, the rest is unread).
 */
int readtext(std::istream & is, std::string & strg, int keepnl = 1,
		const char *eot = ".\r\n", size_t max = 0
    );

#endif

/*
 * Local Variables:
 * mode: c++
 * tab-width: 4
 * c-basic-offset: 4
 * End:
 */
