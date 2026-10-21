# The original NewsCache websites

This folder is an offline copy of NewsCache's two original websites, taken
from the Internet Archive's Wayback Machine. Both sites are long gone: after
the snapshots below the Wayback Machine only has 404s for them.

| Folder | Site | Original URL | Last good snapshot |
|---|---|---|---|
| [`tuwien-1997-2000/`](tuwien-1997-2000/NewsCache/index.html) | Thomas Gschwind, TU Wien (1997 to 2000) | <http://www.infosys.tuwien.ac.at/NewsCache/> | [2008-09-16](https://web.archive.org/web/20080916074415/http://www.infosys.tuwien.ac.at/NewsCache/) |
| [`linuxhacker-2002-2008/`](linuxhacker-2002-2008/NewsCache/index.html) | Herbert Straub, linuxhacker.at (2002 to 2008) | <http://www.linuxhacker.at/NewsCache/> | [2015-09-07](https://web.archive.org/web/20150907023521/http://www.linuxhacker.at/NewsCache/) |

Start pages:

- TU Wien: [`tuwien-1997-2000/NewsCache/index.html`](tuwien-1997-2000/NewsCache/index.html)
- linuxhacker.at: [`linuxhacker-2002-2008/NewsCache/index.html`](linuxhacker-2002-2008/NewsCache/index.html)
- linuxhacker.at, older version of the section (2006 to 2008, at the
  lower-case path `/newscache/`):
  [`linuxhacker-2002-2008/newscache-old/index.html`](linuxhacker-2002-2008/newscache-old/index.html)

The files were fetched on 2026-10-21.

## Copyright and license

**The content is the work and copyright of its original authors**: Thomas
Gschwind (the TU Wien site, his master's thesis and the user's manual; the
USENIX paper is by Thomas Gschwind and Manfred Hauswirth) and Herbert Straub
(the linuxhacker.at pages). It is kept
here unchanged, apart from the link fixes listed below, for historical
reference only.

Neither site states a license for the web pages themselves. What they do say:

- TU Wien pages: footer "© 1997-2000 Thomas Gschwind" (the PSTL page:
  "© 1997-2001"). The NewsCache page says the *software* is "available under
  the GNU General Public License" (linking [`gpl.txt`](tuwien-1997-2000/NewsCache/gpl.txt));
  the PSTL page says PSTL is available "under the terms and conditions of the
  'GNU LIBRARY GENERAL PUBLIC LICENSE'".
- The user's manual ([`doc/manual/`](tuwien-1997-2000/NewsCache/doc/manual/NewsCache_toc.html))
  carries the usual Texinfo notice: "Copyright (C) 1998 Thomas Gschwind",
  "Permission is granted to make and distribute verbatim copies of this manual
  provided the copyright notice and this permission notice are preserved on
  all copies." (plus the standard paragraphs on modified versions and
  translations).
- linuxhacker.at, 2014/2015 pages: no copyright line. The Contribute HowTo
  says about *code*: "Acceptable copyrights for code included in NewsCache is
  the GNU General License."
- linuxhacker.at, older pages: footer "Copyright © 2001-2008 by Herbert
  Straub" (Plone pages) or "Copyright (C) 2001-2006 Herbert Straub" (the
  earlier Drupal pages). The "Distributed under the GNU GPL license" in the
  Plone footer refers to Plone, not to the page content.

NewsCache itself is GPL-2.0-or-later (see [`COPYING`](../../COPYING)); that
does not automatically cover these web pages.

The HICSS-32 paper (`doc/hicss32/` and `download/doc/hicss32.ps*` on the TU
Wien site) is **not** included: its page says "Copyright 1999 IEEE" and that
"permission to reprint/republish this material ... for redistribution to
servers or lists ... must be obtained from the IEEE". Links to it point to the
[Wayback copy](https://web.archive.org/web/20080704173542/http://www.infosys.tuwien.ac.at/NewsCache/doc/hicss32/).

## Layout

Each folder stands for the site's web root and holds only the NewsCache
section plus the stylesheets and images its pages use, at their original
paths:

- `tuwien-1997-2000/NewsCache/...` is `http://www.infosys.tuwien.ac.at/NewsCache/...`.
  A URL ending in `/` is stored as `.../index.html`.
- `linuxhacker-2002-2008/NewsCache/<page>.html` is
  `http://www.linuxhacker.at/NewsCache/<page>` (a Plone site;
  `/NewsCache/` and `/NewsCache/index_html` are the same page, stored as
  `index.html`).
- `linuxhacker-2002-2008/newscache-old/<page>.html` is
  `http://www.linuxhacker.at/newscache/<page>`, the section's earlier version
  (Plone pages last captured 2007 to 2008, four Drupal pages from 2006/2007).
  It is renamed because a folder `newscache` next to `NewsCache` breaks
  checkouts on case-insensitive file systems. The 2014/2015 pages still link
  to its ChangeLog, so those links now point here.
- `linuxhacker-2002-2008/portal_css/`, `themes/` and the images in
  `linuxhacker-2002-2008/` are the site-wide Plone/Drupal files the pages use
  (`portal_css/Plone Default/` is stored as `portal_css/Plone_Default/`).

Folder index pages: the archive is also served as a website, so every folder
without a captured `index.html` has one that was added for the archive (13
pages, written on 2026-10-21). `index.html` in this folder is the landing
page; the others list the folder's files and point to its start page, if it
has one. They are not captures: each starts with the comment
`<!-- folder index added for the archive, not a captured page -->` and isn't
in the list of captures below.

## How it was made

1. The Wayback Machine's CDX API listed every capture under each site's
   NewsCache path, e.g.
   `https://web.archive.org/cdx/search/cdx?url=www.infosys.tuwien.ac.at/NewsCache/*&output=json&fl=timestamp,original,mimetype,statuscode,length`
   (the linuxhacker.at query, `url=www.linuxhacker.at/NewsCache*`, also
   returns the lower-case `/newscache` pages).
2. For each URL the latest capture with status 200 at or before the snapshot
   date above was taken (2008-09-16 for TU Wien, 2015-09-07 for
   linuxhacker.at). Plone URLs with and without a trailing `/` count as one
   page. The table at the end lists every file and its capture.
3. Each file was fetched in the raw form
   (`https://web.archive.org/web/<timestamp>id_/<original URL>`), which
   returns the bytes as originally served, without the Wayback toolbar.
4. Changes to the HTML and CSS files, nothing else:
   - Links to pages and files that are in this copy are relative local links
     (also across the two sites, e.g. linuxhacker.at's link to the TU Wien
     site). `<base href>` is commented out so they resolve locally.
   - `<a href>` links that were relative but point to something not in this
     copy (release tarballs, thesis chapters that were never captured, ...)
     point to `https://web.archive.org/web/<timestamp>/<original URL>`.
     Missing images and stylesheets are left as they were.
   - Absolute links to anything outside the NewsCache section (the rest of
     linuxhacker.at, other sites) are unchanged.
   - The `<script>` elements of the Plone pages, which loaded JavaScript from
     the dead `www.linuxhacker.at`, are commented out
     (`<!-- offline copy: removed <script ...></script> -->`). No JavaScript
     was added; the pages work without it.
   - The character encoding is kept as served: the TU Wien pages are
     ISO-8859-1 (as their `<meta>` tags say), the linuxhacker.at pages are
     UTF-8.

Total size: about 1.9 MB.

## Skipped

- Release tarballs and other archives: none of the `NewsCache-0.9.x` /
  `0.99.x` tarballs, `pstl-*.tar.gz` and `tcp_wrappers_7.6.tar.gz` in the TU
  Wien `download/` folder were ever captured (all 404 in the Wayback Machine);
  `download/addr.tgz` and `download/res.tgz` were captured but skipped
  (`.tgz`). `NewsCache-1.2rc6.tar.gz`, the 1.2rc6 patch and the LDAP patch
  were on `src.linuxhacker.at` and `/linux/downloads/`, outside the section;
  the 1.2rc6 release is tag `1.2rc6` in this repository.
- TU Wien `download/doc/hicss32.ps`, `msthesis.ps`, `usenix99.ps`: identical
  to the `.ps.gz` files that are included (checked after gunzip), 1.8 MB
  uncompressed.
- The HICSS-32 paper (IEEE copyright, see above).
- Apache directory-listing sort variants (`download/?C=N;O=D` and so on).
- Plone "Send this" forms (`.../sendto_form`), `?searchterm=None` URLs (only
  503s were captured), the Plone JavaScript, `IEFixes.css` (only loaded by old
  Internet Explorer) and most of the generic Plone icons the stylesheets
  refer to; only the ones these pages show were fetched.
- The mailing-list archive at `www.linuxhacker.org/cgi-bin/ezmlm-cgi?9:...`
  (Alex Holden's list server, linked from the old Mailinglist page): a
  different site, with about 200 captured pages for this list. The original
  list at TU Wien (newscache@infosys.tuwien.ac.at) had no web archive.
- Herbert Straub's earlier page, <http://members.aon.at/hstraub/linux/newscache/>
  (2002 to 2003, linked from the TU Wien start page): a different site, not
  included.

## Never captured

The Wayback Machine has no good capture of these, so they are missing here
too:

- TU Wien: most of the HTML version of the master's thesis, i.e.
  `doc/msthesis/` chapters `node7`, `8`, `16`, `18`, `20`-`22`, `24`, `26`,
  `32`, `33`, `43`, `45`, `47`, `53`, `54` and `footnode.html`, its stylesheet and all its
  formula images (`img*.gif`). The complete thesis is in
  [`download/doc/msthesis.ps.gz`](tuwien-1997-2000/NewsCache/download/doc/msthesis.ps.gz).
  Also `compatibility.html`, `FAQ.txt`, `texinfo/`, the older
  `ThesisHTML/` copy (only `node29.html` survives, from 1999). The Apache
  icons of the directory listings (`/icons/`, outside the section) are not
  included either.
- linuxhacker.at: the Drupal stylesheets (`misc/drupal.css`,
  `themes/linuxhacker/linuxhacker.css`) of the four Drupal pages, which
  therefore show unstyled.

## Captures used

#### TU Wien

| File | Capture |
|---|---|
| [`NewsCache/ThesisHTML/node29.html`](tuwien-1997-2000/NewsCache/ThesisHTML/node29.html) | [1999-05-08](https://web.archive.org/web/19990508215348/http://www.infosys.tuwien.ac.at/NewsCache/ThesisHTML/node29.html) |
| [`NewsCache/arb.txt`](tuwien-1997-2000/NewsCache/arb.txt) | [2007-07-01](https://web.archive.org/web/20070701032346/http://www.infosys.tuwien.ac.at/NewsCache/arb.txt) |
| [`NewsCache/doc.html`](tuwien-1997-2000/NewsCache/doc.html) | [2008-07-04](https://web.archive.org/web/20080704172501/http://www.infosys.tuwien.ac.at/NewsCache/doc.html) |
| [`NewsCache/doc/manual/NewsCache_1.html`](tuwien-1997-2000/NewsCache/doc/manual/NewsCache_1.html) | [2007-07-02](https://web.archive.org/web/20070702063201/http://www.infosys.tuwien.ac.at/NewsCache/doc/manual/NewsCache_1.html) |
| [`NewsCache/doc/manual/NewsCache_2.html`](tuwien-1997-2000/NewsCache/doc/manual/NewsCache_2.html) | [2007-07-02](https://web.archive.org/web/20070702062858/http://www.infosys.tuwien.ac.at/NewsCache/doc/manual/NewsCache_2.html) |
| [`NewsCache/doc/manual/NewsCache_3.html`](tuwien-1997-2000/NewsCache/doc/manual/NewsCache_3.html) | [2007-07-02](https://web.archive.org/web/20070702063230/http://www.infosys.tuwien.ac.at/NewsCache/doc/manual/NewsCache_3.html) |
| [`NewsCache/doc/manual/NewsCache_4.html`](tuwien-1997-2000/NewsCache/doc/manual/NewsCache_4.html) | [2007-07-02](https://web.archive.org/web/20070702062727/http://www.infosys.tuwien.ac.at/NewsCache/doc/manual/NewsCache_4.html) |
| [`NewsCache/doc/manual/NewsCache_5.html`](tuwien-1997-2000/NewsCache/doc/manual/NewsCache_5.html) | [2007-07-02](https://web.archive.org/web/20070702063239/http://www.infosys.tuwien.ac.at/NewsCache/doc/manual/NewsCache_5.html) |
| [`NewsCache/doc/manual/NewsCache_6.html`](tuwien-1997-2000/NewsCache/doc/manual/NewsCache_6.html) | [2007-07-02](https://web.archive.org/web/20070702063220/http://www.infosys.tuwien.ac.at/NewsCache/doc/manual/NewsCache_6.html) |
| [`NewsCache/doc/manual/NewsCache_toc.html`](tuwien-1997-2000/NewsCache/doc/manual/NewsCache_toc.html) | [2007-07-01](https://web.archive.org/web/20070701032519/http://www.infosys.tuwien.ac.at/NewsCache/doc/manual/NewsCache_toc.html) |
| [`NewsCache/doc/msthesis/NewsCache.html`](tuwien-1997-2000/NewsCache/doc/msthesis/NewsCache.html) | [2008-06-12](https://web.archive.org/web/20080612112153/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/NewsCache.html) |
| [`NewsCache/doc/msthesis/contents_motif.gif`](tuwien-1997-2000/NewsCache/doc/msthesis/contents_motif.gif) | [2007-03-02](https://web.archive.org/web/20070302194614/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/contents_motif.gif) |
| [`NewsCache/doc/msthesis/next_motif.gif`](tuwien-1997-2000/NewsCache/doc/msthesis/next_motif.gif) | [2007-03-02](https://web.archive.org/web/20070302194534/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/next_motif.gif) |
| [`NewsCache/doc/msthesis/node1.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node1.html) | [2007-08-09](https://web.archive.org/web/20070809123132/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node1.html) |
| [`NewsCache/doc/msthesis/node10.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node10.html) | [2008-08-02](https://web.archive.org/web/20080802211223/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node10.html) |
| [`NewsCache/doc/msthesis/node11.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node11.html) | [2007-08-07](https://web.archive.org/web/20070807212803/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node11.html) |
| [`NewsCache/doc/msthesis/node12.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node12.html) | [2007-08-07](https://web.archive.org/web/20070807212821/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node12.html) |
| [`NewsCache/doc/msthesis/node13.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node13.html) | [2007-08-09](https://web.archive.org/web/20070809120530/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node13.html) |
| [`NewsCache/doc/msthesis/node14.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node14.html) | [2007-08-09](https://web.archive.org/web/20070809122822/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node14.html) |
| [`NewsCache/doc/msthesis/node15.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node15.html) | [2007-08-09](https://web.archive.org/web/20070809120856/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node15.html) |
| [`NewsCache/doc/msthesis/node17.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node17.html) | [2006-07-22](https://web.archive.org/web/20060722234008/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node17.html) |
| [`NewsCache/doc/msthesis/node19.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node19.html) | [2007-07-17](https://web.archive.org/web/20070717005155/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node19.html) |
| [`NewsCache/doc/msthesis/node2.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node2.html) | [2008-06-12](https://web.archive.org/web/20080612082831/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node2.html) |
| [`NewsCache/doc/msthesis/node23.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node23.html) | [2007-08-12](https://web.archive.org/web/20070812232423/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node23.html) |
| [`NewsCache/doc/msthesis/node25.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node25.html) | [2007-08-09](https://web.archive.org/web/20070809005846/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node25.html) |
| [`NewsCache/doc/msthesis/node27.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node27.html) | [2007-08-20](https://web.archive.org/web/20070820095050/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node27.html) |
| [`NewsCache/doc/msthesis/node28.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node28.html) | [2007-08-19](https://web.archive.org/web/20070819001815/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node28.html) |
| [`NewsCache/doc/msthesis/node29.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node29.html) | [2008-06-12](https://web.archive.org/web/20080612095225/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node29.html) |
| [`NewsCache/doc/msthesis/node3.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node3.html) | [2007-08-09](https://web.archive.org/web/20070809122835/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node3.html) |
| [`NewsCache/doc/msthesis/node30.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node30.html) | [2007-08-09](https://web.archive.org/web/20070809010523/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node30.html) |
| [`NewsCache/doc/msthesis/node31.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node31.html) | [2008-06-12](https://web.archive.org/web/20080612082832/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node31.html) |
| [`NewsCache/doc/msthesis/node34.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node34.html) | [2008-06-12](https://web.archive.org/web/20080612082837/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node34.html) |
| [`NewsCache/doc/msthesis/node35.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node35.html) | [2007-08-12](https://web.archive.org/web/20070812232250/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node35.html) |
| [`NewsCache/doc/msthesis/node36.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node36.html) | [2008-06-12](https://web.archive.org/web/20080612082843/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node36.html) |
| [`NewsCache/doc/msthesis/node37.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node37.html) | [2007-08-12](https://web.archive.org/web/20070812061116/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node37.html) |
| [`NewsCache/doc/msthesis/node38.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node38.html) | [2007-08-07](https://web.archive.org/web/20070807212750/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node38.html) |
| [`NewsCache/doc/msthesis/node39.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node39.html) | [2007-08-12](https://web.archive.org/web/20070812061312/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node39.html) |
| [`NewsCache/doc/msthesis/node4.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node4.html) | [2007-08-09](https://web.archive.org/web/20070809122744/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node4.html) |
| [`NewsCache/doc/msthesis/node40.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node40.html) | [2007-08-12](https://web.archive.org/web/20070812061038/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node40.html) |
| [`NewsCache/doc/msthesis/node41.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node41.html) | [2007-08-11](https://web.archive.org/web/20070811231209/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node41.html) |
| [`NewsCache/doc/msthesis/node42.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node42.html) | [2007-08-07](https://web.archive.org/web/20070807213718/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node42.html) |
| [`NewsCache/doc/msthesis/node44.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node44.html) | [2007-08-12](https://web.archive.org/web/20070812061240/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node44.html) |
| [`NewsCache/doc/msthesis/node46.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node46.html) | [2007-08-09](https://web.archive.org/web/20070809120829/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node46.html) |
| [`NewsCache/doc/msthesis/node48.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node48.html) | [2007-08-09](https://web.archive.org/web/20070809120723/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node48.html) |
| [`NewsCache/doc/msthesis/node49.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node49.html) | [2008-02-22](https://web.archive.org/web/20080222231223/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node49.html) |
| [`NewsCache/doc/msthesis/node5.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node5.html) | [2007-08-09](https://web.archive.org/web/20070809014622/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node5.html) |
| [`NewsCache/doc/msthesis/node50.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node50.html) | [2008-04-19](https://web.archive.org/web/20080419093901/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node50.html) |
| [`NewsCache/doc/msthesis/node51.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node51.html) | [2008-05-07](https://web.archive.org/web/20080507155622/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node51.html) |
| [`NewsCache/doc/msthesis/node52.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node52.html) | [2008-08-04](https://web.archive.org/web/20080804122359/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node52.html) |
| [`NewsCache/doc/msthesis/node55.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node55.html) | [2008-06-11](https://web.archive.org/web/20080611235033/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node55.html) |
| [`NewsCache/doc/msthesis/node56.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node56.html) | [2008-06-12](https://web.archive.org/web/20080612082900/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node56.html) |
| [`NewsCache/doc/msthesis/node57.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node57.html) | [2007-08-12](https://web.archive.org/web/20070812162525/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node57.html) |
| [`NewsCache/doc/msthesis/node58.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node58.html) | [2007-08-12](https://web.archive.org/web/20070812162001/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node58.html) |
| [`NewsCache/doc/msthesis/node59.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node59.html) | [2007-03-06](https://web.archive.org/web/20070306220317/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node59.html) |
| [`NewsCache/doc/msthesis/node6.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node6.html) | [2008-08-02](https://web.archive.org/web/20080802211228/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node6.html) |
| [`NewsCache/doc/msthesis/node60.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node60.html) | [2008-06-12](https://web.archive.org/web/20080612082906/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node60.html) |
| [`NewsCache/doc/msthesis/node61.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node61.html) | [2007-08-19](https://web.archive.org/web/20070819034353/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node61.html) |
| [`NewsCache/doc/msthesis/node62.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node62.html) | [2007-08-12](https://web.archive.org/web/20070812163246/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node62.html) |
| [`NewsCache/doc/msthesis/node63.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node63.html) | [2007-08-12](https://web.archive.org/web/20070812163946/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node63.html) |
| [`NewsCache/doc/msthesis/node64.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node64.html) | [2007-08-12](https://web.archive.org/web/20070812163325/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node64.html) |
| [`NewsCache/doc/msthesis/node65.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node65.html) | [2007-08-12](https://web.archive.org/web/20070812232431/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node65.html) |
| [`NewsCache/doc/msthesis/node66.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node66.html) | [2007-07-09](https://web.archive.org/web/20070709113123/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node66.html) |
| [`NewsCache/doc/msthesis/node67.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node67.html) | [2008-06-12](https://web.archive.org/web/20080612082912/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node67.html) |
| [`NewsCache/doc/msthesis/node68.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node68.html) | [2008-06-12](https://web.archive.org/web/20080612000952/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node68.html) |
| [`NewsCache/doc/msthesis/node69.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node69.html) | [2008-07-02](https://web.archive.org/web/20080702011519/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node69.html) |
| [`NewsCache/doc/msthesis/node9.html`](tuwien-1997-2000/NewsCache/doc/msthesis/node9.html) | [2008-09-07](https://web.archive.org/web/20080907085136/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/node9.html) |
| [`NewsCache/doc/msthesis/previous_motif.gif`](tuwien-1997-2000/NewsCache/doc/msthesis/previous_motif.gif) | [2007-03-02](https://web.archive.org/web/20070302194705/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/previous_motif.gif) |
| [`NewsCache/doc/msthesis/previous_motif_gr.gif`](tuwien-1997-2000/NewsCache/doc/msthesis/previous_motif_gr.gif) | [2006-07-22](https://web.archive.org/web/20060722111454/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/previous_motif_gr.gif) |
| [`NewsCache/doc/msthesis/up_motif.gif`](tuwien-1997-2000/NewsCache/doc/msthesis/up_motif.gif) | [2007-03-02](https://web.archive.org/web/20070302194544/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/up_motif.gif) |
| [`NewsCache/doc/msthesis/up_motif_gr.gif`](tuwien-1997-2000/NewsCache/doc/msthesis/up_motif_gr.gif) | [2006-07-22](https://web.archive.org/web/20060722234023/http://www.infosys.tuwien.ac.at/NewsCache/doc/msthesis/up_motif_gr.gif) |
| [`NewsCache/doc/usenix99/NewsCacheHP.css`](tuwien-1997-2000/NewsCache/doc/usenix99/NewsCacheHP.css) | [2006-07-21](https://web.archive.org/web/20060721235238/http://www.infosys.tuwien.ac.at/NewsCache/doc/usenix99/NewsCacheHP.css) |
| [`NewsCache/doc/usenix99/img10.gif`](tuwien-1997-2000/NewsCache/doc/usenix99/img10.gif) | [2006-07-21](https://web.archive.org/web/20060721234619/http://www.infosys.tuwien.ac.at/NewsCache/doc/usenix99/img10.gif) |
| [`NewsCache/doc/usenix99/img11.gif`](tuwien-1997-2000/NewsCache/doc/usenix99/img11.gif) | [2006-07-21](https://web.archive.org/web/20060721234637/http://www.infosys.tuwien.ac.at/NewsCache/doc/usenix99/img11.gif) |
| [`NewsCache/doc/usenix99/img12.gif`](tuwien-1997-2000/NewsCache/doc/usenix99/img12.gif) | [2006-07-21](https://web.archive.org/web/20060721234707/http://www.infosys.tuwien.ac.at/NewsCache/doc/usenix99/img12.gif) |
| [`NewsCache/doc/usenix99/img3.gif`](tuwien-1997-2000/NewsCache/doc/usenix99/img3.gif) | [2006-07-21](https://web.archive.org/web/20060721234420/http://www.infosys.tuwien.ac.at/NewsCache/doc/usenix99/img3.gif) |
| [`NewsCache/doc/usenix99/img9.gif`](tuwien-1997-2000/NewsCache/doc/usenix99/img9.gif) | [2006-07-21](https://web.archive.org/web/20060721234439/http://www.infosys.tuwien.ac.at/NewsCache/doc/usenix99/img9.gif) |
| [`NewsCache/doc/usenix99/index.html`](tuwien-1997-2000/NewsCache/doc/usenix99/index.html) | [2008-08-28](https://web.archive.org/web/20080828012545/http://www.infosys.tuwien.ac.at/NewsCache/doc/usenix99/) |
| [`NewsCache/dot.gif`](tuwien-1997-2000/NewsCache/dot.gif) | [2006-08-26](https://web.archive.org/web/20060826014856/http://www.infosys.tuwien.ac.at/NewsCache/dot.gif) |
| [`NewsCache/download.html`](tuwien-1997-2000/NewsCache/download.html) | [2008-07-04](https://web.archive.org/web/20080704172413/http://www.infosys.tuwien.ac.at/NewsCache/download.html) |
| [`NewsCache/download/doc/draft-ietf-nntpext-base-07.txt`](tuwien-1997-2000/NewsCache/download/doc/draft-ietf-nntpext-base-07.txt) | [2008-07-04](https://web.archive.org/web/20080704173454/http://www.infosys.tuwien.ac.at/NewsCache/download/doc/draft-ietf-nntpext-base-07.txt) |
| [`NewsCache/download/doc/index.html`](tuwien-1997-2000/NewsCache/download/doc/index.html) | [2007-07-02](https://web.archive.org/web/20070702103502/http://www.infosys.tuwien.ac.at/NewsCache/download/doc/) |
| [`NewsCache/download/doc/msthesis.ps.gz`](tuwien-1997-2000/NewsCache/download/doc/msthesis.ps.gz) | [2006-07-17](https://web.archive.org/web/20060717181739/http://www.infosys.tuwien.ac.at/NewsCache/download/doc/msthesis.ps.gz) |
| [`NewsCache/download/doc/rfc1036.txt`](tuwien-1997-2000/NewsCache/download/doc/rfc1036.txt) | [2007-12-22](https://web.archive.org/web/20071222135031/http://www.infosys.tuwien.ac.at/NewsCache/download/doc/rfc1036.txt) |
| [`NewsCache/download/doc/rfc977.txt`](tuwien-1997-2000/NewsCache/download/doc/rfc977.txt) | [2008-07-25](https://web.archive.org/web/20080725090247/http://www.infosys.tuwien.ac.at/NewsCache/download/doc/rfc977.txt) |
| [`NewsCache/download/doc/usenix99.ps.gz`](tuwien-1997-2000/NewsCache/download/doc/usenix99.ps.gz) | [2006-07-17](https://web.archive.org/web/20060717182055/http://www.infosys.tuwien.ac.at/NewsCache/download/doc/usenix99.ps.gz) |
| [`NewsCache/download/doc/what_is_usenet.txt`](tuwien-1997-2000/NewsCache/download/doc/what_is_usenet.txt) | [2008-07-04](https://web.archive.org/web/20080704172437/http://www.infosys.tuwien.ac.at/NewsCache/download/doc/what_is_usenet.txt) |
| [`NewsCache/download/index.html`](tuwien-1997-2000/NewsCache/download/index.html) | [2008-08-28](https://web.archive.org/web/20080828012858/http://www.infosys.tuwien.ac.at/NewsCache/download/) |
| [`NewsCache/download/newscache-new.lsm`](tuwien-1997-2000/NewsCache/download/newscache-new.lsm) | [2007-07-02](https://web.archive.org/web/20070702103705/http://www.infosys.tuwien.ac.at/NewsCache/download/newscache-new.lsm) |
| [`NewsCache/download/newscache.lsm`](tuwien-1997-2000/NewsCache/download/newscache.lsm) | [2007-07-02](https://web.archive.org/web/20070702103612/http://www.infosys.tuwien.ac.at/NewsCache/download/newscache.lsm) |
| [`NewsCache/general.html`](tuwien-1997-2000/NewsCache/general.html) | [2008-02-23](https://web.archive.org/web/20080223001444/http://www.infosys.tuwien.ac.at/NewsCache/general.html) |
| [`NewsCache/gpl.txt`](tuwien-1997-2000/NewsCache/gpl.txt) | [2008-08-28](https://web.archive.org/web/20080828013430/http://www.infosys.tuwien.ac.at/NewsCache/gpl.txt) |
| [`NewsCache/index.html`](tuwien-1997-2000/NewsCache/index.html) | [2008-09-16](https://web.archive.org/web/20080916074415/http://www.infosys.tuwien.ac.at/NewsCache/) |
| [`NewsCache/newscache.html`](tuwien-1997-2000/NewsCache/newscache.html) | [2008-07-04](https://web.archive.org/web/20080704173005/http://www.infosys.tuwien.ac.at/NewsCache/newscache.html) |
| [`NewsCache/pstl.html`](tuwien-1997-2000/NewsCache/pstl.html) | [2008-08-28](https://web.archive.org/web/20080828012423/http://www.infosys.tuwien.ac.at/NewsCache/pstl.html) |
| [`NewsCache/students.html`](tuwien-1997-2000/NewsCache/students.html) | [2008-07-04](https://web.archive.org/web/20080704172701/http://www.infosys.tuwien.ac.at/NewsCache/students.html) |
| [`NewsCache/usenix.png`](tuwien-1997-2000/NewsCache/usenix.png) | [2006-08-21](https://web.archive.org/web/20060821154806/http://www.infosys.tuwien.ac.at/NewsCache/usenix.png) |

#### linuxhacker.at

| File | Capture |
|---|---|
| [`NewsCache/bugs-and-restrictions.html`](linuxhacker-2002-2008/NewsCache/bugs-and-restrictions.html) | [2014-05-05](https://web.archive.org/web/20140505010923/http://www.linuxhacker.at/NewsCache/bugs-and-restrictions) |
| [`NewsCache/changelog.html`](linuxhacker-2002-2008/NewsCache/changelog.html) | [2014-07-02](https://web.archive.org/web/20140702204241/http://www.linuxhacker.at/NewsCache/changelog) |
| [`NewsCache/contribute-howto.html`](linuxhacker-2002-2008/NewsCache/contribute-howto.html) | [2014-05-05](https://web.archive.org/web/20140505010934/http://www.linuxhacker.at/NewsCache/contribute-howto) |
| [`NewsCache/cvs-access.html`](linuxhacker-2002-2008/NewsCache/cvs-access.html) | [2014-05-05](https://web.archive.org/web/20140505010939/http://www.linuxhacker.at/NewsCache/cvs-access) |
| [`NewsCache/documentation.html`](linuxhacker-2002-2008/NewsCache/documentation.html) | [2014-05-05](https://web.archive.org/web/20140505010944/http://www.linuxhacker.at/NewsCache/documentation) |
| [`NewsCache/download.html`](linuxhacker-2002-2008/NewsCache/download.html) | [2015-08-04](https://web.archive.org/web/20150804054140/http://www.linuxhacker.at/NewsCache/download) |
| [`NewsCache/feature-requests.html`](linuxhacker-2002-2008/NewsCache/feature-requests.html) | [2014-05-05](https://web.archive.org/web/20140505010954/http://www.linuxhacker.at/NewsCache/feature-requests) |
| [`NewsCache/index.html`](linuxhacker-2002-2008/NewsCache/index.html) | [2015-09-07](https://web.archive.org/web/20150907023521/http://www.linuxhacker.at/NewsCache/) |
| [`NewsCache/links.html`](linuxhacker-2002-2008/NewsCache/links.html) | [2014-05-05](https://web.archive.org/web/20140505011005/http://www.linuxhacker.at/NewsCache/links) |
| [`NewsCache/news.html`](linuxhacker-2002-2008/NewsCache/news.html) | [2014-07-24](https://web.archive.org/web/20140724085012/http://www.linuxhacker.at/NewsCache/news) |
| [`NewsCache/sitemap.html`](linuxhacker-2002-2008/NewsCache/sitemap.html) | [2014-05-05](https://web.archive.org/web/20140505011010/http://www.linuxhacker.at/NewsCache/sitemap) |
| [`NewsCache/status.html`](linuxhacker-2002-2008/NewsCache/status.html) | [2014-07-29](https://web.archive.org/web/20140729040007/http://www.linuxhacker.at/NewsCache/status) |
| [`arrowLeft.gif`](linuxhacker-2002-2008/arrowLeft.gif) | [2015-09-05](https://web.archive.org/web/20150905170105/http://www.linuxhacker.at/arrowLeft.gif) |
| [`arrowRight.gif`](linuxhacker-2002-2008/arrowRight.gif) | [2015-09-05](https://web.archive.org/web/20150905091401/http://www.linuxhacker.at/arrowRight.gif) |
| [`arrowUp.gif`](linuxhacker-2002-2008/arrowUp.gif) | [2015-09-05](https://web.archive.org/web/20150905151022/http://www.linuxhacker.at/arrowUp.gif) |
| [`bullet.gif`](linuxhacker-2002-2008/bullet.gif) | [2015-09-05](https://web.archive.org/web/20150905151031/http://www.linuxhacker.at/bullet.gif) |
| [`favicon.ico`](linuxhacker-2002-2008/favicon.ico) | [2015-09-05](https://web.archive.org/web/20150905092335/http://www.linuxhacker.at/favicon.ico) |
| [`info_icon.gif`](linuxhacker-2002-2008/info_icon.gif) | [2007-07-14](https://web.archive.org/web/20070714202346/http://www.linuxhacker.at/info_icon.gif) |
| [`input_background.gif`](linuxhacker-2002-2008/input_background.gif) | [2015-09-05](https://web.archive.org/web/20150905152116/http://www.linuxhacker.at/input_background.gif) |
| [`linkOpaque.gif`](linuxhacker-2002-2008/linkOpaque.gif) | [2015-09-05](https://web.archive.org/web/20150905124749/http://www.linuxhacker.at/linkOpaque.gif) |
| [`linkTransparent.gif`](linuxhacker-2002-2008/linkTransparent.gif) | [2015-09-05](https://web.archive.org/web/20150905151016/http://www.linuxhacker.at/linkTransparent.gif) |
| [`link_icon.gif`](linuxhacker-2002-2008/link_icon.gif) | [2015-09-05](https://web.archive.org/web/20150905145714/http://www.linuxhacker.at/link_icon.gif) |
| [`linuxhackerlogo.png`](linuxhacker-2002-2008/linuxhackerlogo.png) | [2015-09-05](https://web.archive.org/web/20150905091927/http://www.linuxhacker.at/linuxhackerlogo.png) |
| [`mail_icon.gif`](linuxhacker-2002-2008/mail_icon.gif) | [2007-07-14](https://web.archive.org/web/20070714202539/http://www.linuxhacker.at/mail_icon.gif) |
| [`newscache-old/bugs.html`](linuxhacker-2002-2008/newscache-old/bugs.html) | [2007-10-29](https://web.archive.org/web/20071029220829/http://www.linuxhacker.at/newscache/bugs) |
| [`newscache-old/changelog.html`](linuxhacker-2002-2008/newscache-old/changelog.html) | [2007-10-29](https://web.archive.org/web/20071029220835/http://www.linuxhacker.at/newscache/changelog) |
| [`newscache-old/contribute.html`](linuxhacker-2002-2008/newscache-old/contribute.html) | [2007-10-29](https://web.archive.org/web/20071029220839/http://www.linuxhacker.at/newscache/contribute) |
| [`newscache-old/cvs.html`](linuxhacker-2002-2008/newscache-old/cvs.html) | [2007-04-15](https://web.archive.org/web/20070415104823/http://www.linuxhacker.at/newscache/cvs) |
| [`newscache-old/cvsaccess.html`](linuxhacker-2002-2008/newscache-old/cvsaccess.html) | [2007-10-29](https://web.archive.org/web/20071029220843/http://www.linuxhacker.at/newscache/cvsaccess) |
| [`newscache-old/documentation.html`](linuxhacker-2002-2008/newscache-old/documentation.html) | [2008-02-23](https://web.archive.org/web/20080223144213/http://www.linuxhacker.at/newscache/documentation) |
| [`newscache-old/download.html`](linuxhacker-2002-2008/newscache-old/download.html) | [2008-01-22](https://web.archive.org/web/20080122035703/http://www.linuxhacker.at/newscache/download) |
| [`newscache-old/enabling_cookies.html`](linuxhacker-2002-2008/newscache-old/enabling_cookies.html) | [2007-10-30](https://web.archive.org/web/20071030022501/http://www.linuxhacker.at/newscache/enabling_cookies) |
| [`newscache-old/feature.html`](linuxhacker-2002-2008/newscache-old/feature.html) | [2008-01-22](https://web.archive.org/web/20080122035712/http://www.linuxhacker.at/newscache/feature) |
| [`newscache-old/feature_requests.html`](linuxhacker-2002-2008/newscache-old/feature_requests.html) | [2007-04-16](https://web.archive.org/web/20070416114321/http://www.linuxhacker.at/newscache/feature_requests) |
| [`newscache-old/index.html`](linuxhacker-2002-2008/newscache-old/index.html) | [2008-03-11](https://web.archive.org/web/20080311072233/http://www.linuxhacker.at/newscache) |
| [`newscache-old/links.html`](linuxhacker-2002-2008/newscache-old/links.html) | [2007-10-29](https://web.archive.org/web/20071029220904/http://www.linuxhacker.at/newscache/links) |
| [`newscache-old/mailinglist.html`](linuxhacker-2002-2008/newscache-old/mailinglist.html) | [2007-10-29](https://web.archive.org/web/20071029220910/http://www.linuxhacker.at/newscache/mailinglist) |
| [`newscache-old/news.html`](linuxhacker-2002-2008/newscache-old/news.html) | [2008-02-23](https://web.archive.org/web/20080223143937/http://www.linuxhacker.at/newscache/news) |
| [`newscache-old/release-1.2.rc5.html`](linuxhacker-2002-2008/newscache-old/release-1.2.rc5.html) | [2006-09-25](https://web.archive.org/web/20060925111643/http://www.linuxhacker.at/newscache/release-1.2.rc5) |
| [`newscache-old/release-1.2rc6.html`](linuxhacker-2002-2008/newscache-old/release-1.2rc6.html) | [2006-09-25](https://web.archive.org/web/20060925111508/http://www.linuxhacker.at/newscache/release-1.2rc6) |
| [`newscache-old/status.html`](linuxhacker-2002-2008/newscache-old/status.html) | [2008-02-23](https://web.archive.org/web/20080223143949/http://www.linuxhacker.at/newscache/status) |
| [`portal_css/LinuxhackerSkin/base-cachekey9117.css`](linuxhacker-2002-2008/portal_css/LinuxhackerSkin/base-cachekey9117.css) | [2015-09-05](https://web.archive.org/web/20150905093143/http://www.linuxhacker.at/portal_css/LinuxhackerSkin/base-cachekey9117.css) |
| [`portal_css/LinuxhackerSkin/resourcelinuxhackerskin.stylesheetsmain-cachekey9894.css`](linuxhacker-2002-2008/portal_css/LinuxhackerSkin/resourcelinuxhackerskin.stylesheetsmain-cachekey9894.css) | [2015-09-05](https://web.archive.org/web/20150905084708/http://www.linuxhacker.at/portal_css/LinuxhackerSkin/resourcelinuxhackerskin.stylesheetsmain-cachekey9894.css) |
| [`portal_css/Plone_Default/ploneStyles2419.css`](linuxhacker-2002-2008/portal_css/Plone_Default/ploneStyles2419.css) | [2007-07-14](https://web.archive.org/web/20070714202504/http://www.linuxhacker.at/portal_css/Plone%20Default/ploneStyles2419.css) |
| [`portal_css/Plone_Default/ploneStyles8098.css`](linuxhacker-2002-2008/portal_css/Plone_Default/ploneStyles8098.css) | [2007-07-14](https://web.archive.org/web/20070714202739/http://www.linuxhacker.at/portal_css/Plone%20Default/ploneStyles8098.css) |
| [`print_icon.gif`](linuxhacker-2002-2008/print_icon.gif) | [2007-07-14](https://web.archive.org/web/20070714202429/http://www.linuxhacker.at/print_icon.gif) |
| [`search_icon.gif`](linuxhacker-2002-2008/search_icon.gif) | [2015-09-05](https://web.archive.org/web/20150905145728/http://www.linuxhacker.at/search_icon.gif) |
| [`themes/linuxhacker/images/icon.png`](linuxhacker-2002-2008/themes/linuxhacker/images/icon.png) | [2007-02-06](https://web.archive.org/web/20070206141427/http://www.linuxhacker.at/themes/linuxhacker/images/icon.png) |
| [`themes/linuxhacker/images/logo.png`](linuxhacker-2002-2008/themes/linuxhacker/images/logo.png) | [2007-02-06](https://web.archive.org/web/20070206100126/http://www.linuxhacker.at/themes/linuxhacker/images/logo.png) |
| [`user.gif`](linuxhacker-2002-2008/user.gif) | [2010-10-01](https://web.archive.org/web/20101001232503/http://www.linuxhacker.at/user.gif) |
