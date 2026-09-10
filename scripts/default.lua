-- Nimfadora call script
-- Autonomous Telephony Flow Definition

agent = {
    name = "Nimfadora",
    language = "en",
    intro = "Hello, I am Nimfadora TARDIS, an AI assistant.",
}

function greet()
    return {
        action = "say",
        text = agent.intro .. " How can I help you today?",
        next = "listen_for_response"
    }
end

function listen_for_response()
    return { action = "listen", timeout = 10, next = "handle_response" }
end

function handle_response()
    return {
        action = "ask_brain",
        system_prompt = "You are a helpful phone assistant. Keep answers concise.",
        next = "speak_reply"
    }
end

function speak_reply()
    return { action = "say", next = "listen_for_response" }
end

function hangup()
    return {
        action = "say",
        text = "Thank you, have a wonderful day.",
        next = "disconnect"
    }
end

function get_flow()
    return greet()
end
