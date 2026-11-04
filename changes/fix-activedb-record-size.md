Bug fixes:
* A damaged record in the active database that was a few bytes longer
  than a group entry (554 to 560 bytes) was not recognised as damage:
  GROUP and the other commands for that group failed with an error
  until the file was removed by hand. The file is now moved aside and
  rebuilt, as for any other damaged record.
