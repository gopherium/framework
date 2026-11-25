Feature: Rules a built app and a public folder share
  A built app and a public folder follow the same rules. A path segment
  that starts with a dot names a hidden file, such as .env or .git, and
  answers not found whether or not the file is there. The one exception
  is .well-known, the folder the web keeps for public files such as
  security.txt. A path with an empty segment answers not found. Only GET
  and HEAD are answered, and any other method gets 405 with the methods
  allowed. A file gets the type its name gives, from a table that is the
  same on every system, and a name the table does not know gets a type
  that makes the browser download the file. Every answer tells the
  browser never to guess a type from the bytes. The caller may answer
  not found in its own way.

  @wip
  Scenario Outline: A hidden file is never sent
    Given a <source> that holds "index.html", ".env", ".git/config" and "docs/.draft.md"
    When a browser asks for "<path>"
    Then the answer is not found

    Examples:
      | source        | path            |
      | built app     | /.env           |
      | built app     | /.git/config    |
      | built app     | /.git/          |
      | built app     | /../go.mod      |
      | built app     | /%2e%2e/go.mod  |
      | public folder | /.env           |
      | public folder | /docs/.draft.md |
      | public folder | /docs/../.env   |

  @wip
  Scenario Outline: A file under .well-known is sent
    Given a <source> that holds "index.html" and ".well-known/security.txt"
    When a browser asks for "/.well-known/security.txt"
    Then the answer is the file ".well-known/security.txt" with the type "text/plain; charset=utf-8"

    Examples:
      | source        |
      | built app     |
      | public folder |

  @wip
  Scenario Outline: A path with an empty segment answers not found
    Given a <source> that holds "index.html" and "assets/index-DWLUpakq.js"
    When a browser asks for "<path>"
    Then the answer is not found

    Examples:
      | source        | path                       |
      | built app     | //assets/index-DWLUpakq.js |
      | built app     | /posts//42                 |
      | public folder | //index.html               |
      | public folder | /assets//index-DWLUpakq.js |

  @wip
  Scenario Outline: Only GET and HEAD are answered
    Given a <source> that holds "index.html" and "favicon.svg"
    When a browser asks for "<path>" with the method "<method>"
    Then the answer is 405 allowing "GET, HEAD"

    Examples:
      | source        | method | path         |
      | built app     | POST   | /            |
      | built app     | DELETE | /posts/42    |
      | built app     | PUT    | /favicon.svg |
      | public folder | POST   | /favicon.svg |
      | public folder | PATCH  | /missing.png |

  @wip
  Scenario: HEAD gets the headers of the page without its body
    Given a built app that holds "index.html"
    When a browser asks for "/posts/42" with the method "HEAD"
    Then the answer carries "Content-Type: text/html; charset=utf-8"
    And the answer carries the length of the page
    And the answer has no body

  @wip
  Scenario Outline: A file gets the type its name gives, the same on every system
    Given a public folder that holds "<name>"
    When a browser asks for "/<name>"
    Then the answer is the file "<name>" with the type "<type>"

    Examples:
      | name               | type                      |
      | robots.txt         | text/plain; charset=utf-8 |
      | fonts/inter.woff2  | font/woff2                |
      | media/clip.mp4     | video/mp4                 |
      | site.webmanifest   | application/manifest+json |
      | notes              | application/octet-stream  |
      | report.unknowntype | application/octet-stream  |

  @wip
  Scenario: A file that looks like HTML but whose name gives no type is sent as a download
    Given a public folder that holds "notes" that reads:
      """
      <!doctype html><script>window.loaded = true</script>
      """
    When a browser asks for "/notes"
    Then the answer is the file "notes" with the type "application/octet-stream"

  @wip
  Scenario Outline: Every answer tells the browser never to guess a type
    Given a <source> that holds "index.html", "favicon.svg" and "assets/index-DWLUpakq.js"
    When a browser asks for "<path>"
    Then the answer carries "X-Content-Type-Options: nosniff"

    Examples:
      | source        | path                      |
      | built app     | /posts/42                 |
      | built app     | /assets/index-DWLUpakq.js |
      | built app     | /assets/index-0ldHa5h1.js |
      | built app     | /.env                     |
      | public folder | /favicon.svg              |
      | public folder | /missing.png              |

  @wip
  Scenario Outline: The caller may answer not found in its own way
    Given a <source> that holds "index.html" and "favicon.svg"
    And the caller answers not found in its own way
    When a browser asks for "<path>"
    Then the caller's own answer is sent

    Examples:
      | source        | path                      |
      | built app     | /assets/index-0ldHa5h1.js |
      | built app     | /.env                     |
      | public folder | /missing.png              |
      | public folder | /.env                     |
