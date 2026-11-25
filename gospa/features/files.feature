Feature: Serving a public folder
  A public folder, such as the files people upload or the static files of
  a site, is served file by file. There is no page to fall back to, so a
  path that names no file answers not found. A folder is never listed,
  and its index.html is sent only when asked for by name. The browser
  checks every file with the server on every load.

  Background:
    Given a public folder that holds "logo.png", "docs/guide.pdf" and "docs/index.html"

  Scenario: A file is sent as itself and checked on every load
    When a browser asks for "/logo.png"
    Then the answer is the file "logo.png" with the type "image/png"
    And the answer carries "Cache-Control: no-cache"

  Scenario Outline: A path that names no file answers not found
    When a browser asks for "<path>"
    Then the answer is not found

    Examples:
      | path         |
      | /missing.png |
      | /            |
      | /docs        |
      | /docs/       |

  Scenario: An index.html is sent when asked for by name
    When a browser asks for "/docs/index.html"
    Then the answer is the file "docs/index.html" with the type "text/html; charset=utf-8"

  Scenario: A part of a file is sent when asked
    When a browser asks for "/docs/guide.pdf" with the range "bytes=0-3"
    Then the answer is 206 holding the first 4 bytes of "docs/guide.pdf"
