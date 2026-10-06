class Dietapp < Formula
  desc "AI-powered nutrition assistant with pantry management and meal planning"
  homepage "https://github.com/Jayhub27/diet-app-go"
  url "https://github.com/Jayhub27/diet-app-go/releases/download/v1.0.0/dietapp-darwin-arm64.tar.gz"
  sha256 "SHA256_PLACEHOLDER"
  license "MIT"
  version "1.0.0"

  on_macos do
    if Hardware::CPU.intel?
      url "https://github.com/Jayhub27/diet-app-go/releases/download/v1.0.0/dietapp-darwin-amd64.tar.gz"
      sha256 "SHA256_PLACEHOLDER_INTEL"
    end
  end

  def install
    arch = Hardware::CPU.arm? ? "arm64" : "amd64"
    bin.install "dietapp-darwin-#{arch}" => "dietapp"
    
    # Create default config directory
    (prefix/"etc").mkpath
    cp ".env.example", prefix/"etc/dietapp.env.example" if File.exist?(".env.example")
  end

  def post_install
    ohai "DietApp installed successfully!"
    ohai "Run 'dietapp --init-config' to create your configuration"
    ohai "Then run 'dietapp' to start the server"
    ohai "Open http://localhost:8080 in your browser"
  end

  def caveats
    <<~EOS
      DietApp requires an AI provider API key (OpenAI, OpenRouter, etc.)
      or a local AI model (Ollama, LM Studio) to function.
      
      Quick start:
        1. dietapp --init-config
        2. Edit ~/.dietapp/.env with your API keys
        3. dietapp
        4. Open http://localhost:8080
      
      For local AI (no API key needed):
        Install Ollama: brew install ollama
        Pull a model: ollama pull llama3.1:8b
        Set DEFAULT_AI_SOURCE=ollama in ~/.dietapp/.env
    EOS
  end

  service do
    run [opt_bin/"dietapp"]
    keep_alive true
    working_dir HOMEBREW_PREFIX
    log_path var/"log/dietapp.log"
    error_log_path var/"log/dietapp.error.log"
  end

  test do
    system "#{bin}/dietapp", "--version"
  end
end
