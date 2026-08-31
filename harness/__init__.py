"""Scoring harness package marker.

Present because `python3 -m unittest discover -s harness -t .` refuses a start
directory that is not an importable package: Python 3.11 dropped namespace
package support from unittest discovery. The modules themselves are also
runnable as scripts, so they import each other by path rather than through this
package.
"""
