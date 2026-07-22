class Ntx < Formula
  desc "Self-hosted personal site server: blog, content sections, admin UI"
  homepage "https://github.com/nerdtx/ntx"
  version "0.7.1"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/nerdtx/ntx/releases/download/v#{version}/ntx-darwin-arm64"
      sha256 "" # fill in after release
    end
    on_intel do
      url "https://github.com/nerdtx/ntx/releases/download/v#{version}/ntx-darwin-amd64"
      sha256 "" # fill in after release
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/nerdtx/ntx/releases/download/v#{version}/ntx-linux-arm64"
      sha256 "" # fill in after release
    end
    on_intel do
      url "https://github.com/nerdtx/ntx/releases/download/v#{version}/ntx-linux-amd64"
      sha256 "" # fill in after release
    end
  end

  def install
    bin.install Dir["ntx-*"].first => "ntx"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/ntx version")
  end
end
