import Foundation

/// Base mock URLProtocol — subclass per test file to get isolated static handlers.
/// Subclasses override `requestHandler` to provide their isolated handler.
class BaseMockProtocol: URLProtocol, @unchecked Sendable {
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    class var requestHandler: ((URLRequest) throws -> (HTTPURLResponse, Data))? { nil }

    override func startLoading() {
        guard let handler = Self.requestHandler else {
            client?.urlProtocol(self, didFailWithError: URLError(.unknown))
            return
        }
        do {
            let (response, data) = try handler(request)
            client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: data)
            client?.urlProtocolDidFinishLoading(self)
        } catch {
            client?.urlProtocol(self, didFailWithError: error)
        }
    }
}

/// URLProtocol base that normalises the request body.
///
/// URLSession promotes `httpBody` to `httpBodyStream` when routing through a
/// URLProtocol subclass. This base reads the stream and passes the raw bytes as
/// a separate `Data?` argument so handlers can inspect POST/PATCH bodies.
class BodyAwareBaseMockProtocol: URLProtocol, @unchecked Sendable {
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    class var requestHandler: ((URLRequest, Data?) throws -> (HTTPURLResponse, Data))? { nil }

    override func startLoading() {
        guard let handler = Self.requestHandler else {
            client?.urlProtocol(self, didFailWithError: URLError(.unknown))
            return
        }
        var bodyData: Data?
        if let stream = request.httpBodyStream {
            stream.open()
            var data = Data()
            let bufferSize = 4096
            let buffer = UnsafeMutablePointer<UInt8>.allocate(capacity: bufferSize)
            defer { buffer.deallocate() }
            while stream.hasBytesAvailable {
                let read = stream.read(buffer, maxLength: bufferSize)
                if read > 0 {
                    data.append(buffer, count: read)
                } else if read == 0 {
                    break  // EOF
                } else {
                    // read < 0 means a stream error — fail the request
                    let error = stream.streamError ?? URLError(.cannotDecodeRawData)
                    stream.close()
                    client?.urlProtocol(self, didFailWithError: error)
                    return  // defer deallocates buffer
                }
            }
            stream.close()
            bodyData = data.isEmpty ? nil : data
        } else {
            bodyData = request.httpBody
        }
        do {
            let (response, data) = try handler(request, bodyData)
            client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: data)
            client?.urlProtocolDidFinishLoading(self)
        } catch {
            client?.urlProtocol(self, didFailWithError: error)
        }
    }
}

// Each test file gets its own subclass with an isolated static handler.

final class StatusPollerMock: BaseMockProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var handler: ((URLRequest) throws -> (HTTPURLResponse, Data))?
    override class var requestHandler: ((URLRequest) throws -> (HTTPURLResponse, Data))? { handler }
}

final class DataBrowserMock: BaseMockProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var handler: ((URLRequest) throws -> (HTTPURLResponse, Data))?
    override class var requestHandler: ((URLRequest) throws -> (HTTPURLResponse, Data))? { handler }
}

final class DataBrowserBodyMock: BodyAwareBaseMockProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var handler: ((URLRequest, Data?) throws -> (HTTPURLResponse, Data))?
    override class var requestHandler: ((URLRequest, Data?) throws -> (HTTPURLResponse, Data))? { handler }
}

final class PreferencesMock: BodyAwareBaseMockProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var handler: ((URLRequest, Data?) throws -> (HTTPURLResponse, Data))?
    override class var requestHandler: ((URLRequest, Data?) throws -> (HTTPURLResponse, Data))? { handler }
}

final class ProfileEditorMock: BodyAwareBaseMockProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var handler: ((URLRequest, Data?) throws -> (HTTPURLResponse, Data))?
    override class var requestHandler: ((URLRequest, Data?) throws -> (HTTPURLResponse, Data))? { handler }
}

func makeSession(for protocolClass: AnyClass) -> URLSession {
    let config = URLSessionConfiguration.ephemeral
    config.protocolClasses = [protocolClass]
    return URLSession(configuration: config)
}
