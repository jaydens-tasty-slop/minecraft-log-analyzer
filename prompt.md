Build an ETL pipeline written in golang that parses server logs from MInecraft so we can build interesting reports. Will be run as a cronjob daily, needs to parse log lines exactly once and ungzip them as needed. Save parsed events in a SQLite database for easy storage/querying/building reports later. Then build static HTML reports with golang’s HTML templater. 

Most of the reports we want are player centric. SHould include (but not limited to)
* Number of sessions
* Total number of hours spent on server (be robust against missing logs/ crashes)
* Total number of chat messages sent
* Total number of commands run
* If reasonable, the number of hours spend playing with at least one other player online. Calculate a “loaner percentage”
* Last login
* First login
* Swear word count

Server level statistics:
* Number of restarts
* Number of crashes
* peak number of players online
* Last time a player was on

Brainstorm and augment these as needed.

UI

Have one html page that is a sortable table of these stats, sorted by last login time by default. Generate a page for each player and link to it from the table with more detailed statistics.
4:09 PM

For each player, identify every player that they have been on the server at the same time as and compute (1) the number of shared play sessions and (2) number of hours playing with each. Include this list of players in the detailed statistics page

