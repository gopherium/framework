Feature: Serving a built single-page app
  A built single-page app is one page, index.html, and the files its build
  wrote beside it. The app's own router reads the address in the browser,
  so every path that names no file gets the page, a missing file outside
  assets/ included. The build writes its scripts, styles and fonts under
  assets/, and only the page asks for them, so a path there that names no
  file answers not found instead. A page from an older build that asks for
  a file a newer build replaced then gets an error it can handle, never
  HTML it would run as a script. A folder is never listed. The page is
  read again each time it is sent, so a new build shows without a restart.

  Background:
    Given a built app that holds "index.html", "favicon.svg", "images/logo.png" and "assets/index-DWLUpakq.js"

  @wip
  Scenario Outline: Every path that names no file gets the page
    When a browser asks for "<path>"
    Then the answer is the page

    Examples:
      | path                |
      | /                   |
      | /posts/42           |
      | /settings/          |
      | /images             |
      | /images/            |
      | /images/missing.png |

  @wip
  Scenario: A file the build wrote is sent as itself
    When a browser asks for "/images/logo.png"
    Then the answer is the file "images/logo.png" with the type "image/png"

  @wip
  Scenario Outline: A path under assets that names no file answers not found
    When a browser asks for "<path>"
    Then the answer is not found

    Examples:
      | path                      |
      | /assets/index-0ldHa5h1.js |
      | /assets/fonts/            |
      | /assets/                  |
      | /assets                   |

  @wip
  Scenario: A page the build writes again is sent without a restart
    When a browser asks for "/posts/42"
    And the build writes "index.html" again as:
      """
      <!doctype html><title>Rebuilt</title>
      """
    And a browser asks for "/posts/42"
    Then the answer reads:
      """
      <!doctype html><title>Rebuilt</title>
      """

  @wip
  Scenario: The app is not set up when its build holds no page
    Given the build holds no "index.html"
    When the app is set up
    Then setting it up fails naming "index.html"
