Feature: How long a browser keeps each file of a built app
  A file under assets/ whose name ends in a hash of its content never
  changes under that name, so a browser may keep it for a set window
  without asking again. The hash is the 8 letters, digits, dashes or
  underscores the build puts after a dash, just before the extension, in
  any folder under assets/. The caller sets the
  window, and it is one year when none is set. The page and every other
  file may be kept too, but the browser checks each one with the server
  on every load, so a new build shows at once. A check costs a 304 with
  no body while nothing changed. A file is checked by its date when it has
  one, and by a tag of its content when it has none, as files embedded in
  the program do. The page is always checked by a tag of the bytes sent,
  never by the date of its file.

  Background:
    Given a built app that holds "index.html", "favicon.svg", "favicon-DWLUpakq.svg", "assets/logo.svg", "assets/index-DWLUpakq.js", "assets/vendor-a_B-9xYz.js" and "assets/index-DWLUpak.js"

  Scenario Outline: Only a file under assets with a hash in its name is kept without asking
    When a browser asks for "<path>"
    Then the answer carries "Cache-Control: <rule>"

    Examples:
      | path                       | rule                                |
      | /assets/index-DWLUpakq.js  | public, max-age=31536000, immutable |
      | /assets/vendor-a_B-9xYz.js | public, max-age=31536000, immutable |
      | /assets/index-DWLUpak.js   | no-cache                            |
      | /assets/logo.svg           | no-cache                            |
      | /favicon-DWLUpakq.svg      | no-cache                            |
      | /favicon.svg               | no-cache                            |
      | /                          | no-cache                            |
      | /posts/42                  | no-cache                            |

  Scenario: The caller sets a shorter window for hashed files
    Given the window for hashed files is 1 hour
    When a browser asks for "/assets/index-DWLUpakq.js"
    Then the answer carries "Cache-Control: public, max-age=3600, immutable"

  Scenario: A file with a date is checked by its date
    Given the files were last changed at "2026-11-01T10:00:00Z"
    When a browser asks for "/favicon.svg"
    Then the answer carries "Last-Modified: Sun, 01 Nov 2026 10:00:00 GMT"
    When the browser asks for "/favicon.svg" again with the date it got
    Then the answer is 304 with no body

  Scenario: A file with no date is checked by a tag of its content
    Given the files carry no date, as embedded files do
    When a browser asks for "/favicon.svg"
    Then the answer carries a tag
    When the browser asks for "/favicon.svg" again with the tag it got
    Then the answer is 304 with no body

  Scenario: A file with no date that changes is sent again
    Given the files carry no date, as embedded files do
    When a browser asks for "/favicon.svg"
    Then the answer carries a tag
    When the build writes "favicon.svg" again as:
      """
      <svg>a new logo</svg>
      """
    And the browser asks for "/favicon.svg" again with the tag it got
    Then the answer reads:
      """
      <svg>a new logo</svg>
      """

  Scenario: The page is checked by a tag of the bytes sent, never by its date
    Given the files were last changed at "2026-11-01T10:00:00Z"
    When a browser asks for "/posts/42"
    Then the answer carries a tag
    And the answer carries no date
    When the browser asks for "/posts/42" again with the tag it got
    Then the answer is 304 with no body
