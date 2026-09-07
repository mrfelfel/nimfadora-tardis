-- Nimfadora call script
-- Edit this to change call behavior

agent = {
    name = "Nimfadora",
    language = "fa",
    intro = "سلام، من نمیفادورا هستم، یک هوش مصنوعی.",
}

function greet()
    return {
        action = "say",
        text = agent.intro .. " می‌تونم کمکتون کنم؟",
        next = "listen_for_response"
    }
end

function listen_for_response()
    return { action = "listen", timeout = 10, next = "handle_response" }
end

function handle_response()
    return {
        action = "ask_brain",
        system_prompt = "تو یک دستیار تلفنی هستی. پاسخ کوتاه بده.",
        next = "speak_reply"
    }
end

function speak_reply()
    return { action = "say", next = "listen_for_response" }
end

function hangup()
    return {
        action = "say",
        text = "ممنون، روز خوبی داشته باشید.",
        next = "disconnect"
    }
end

function get_flow()
    return greet()
end
