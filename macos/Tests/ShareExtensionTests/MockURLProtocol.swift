import Foundation

/// Base mock URLProtocol for the ShareExtension test target.
///
/// When URLSession passes a request through a URLProtocol subclass, `httpBody`
/// is promoted to `httpBodyStream`. This base class normalises the body back
/// onto a plain `Data` value before handing off to the handler.
class BaseMockProtocol: URLProtocol, @unchecked Sendable {
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    class var requestHandler: ((URLRequest, Data?) throws -> (HTTPURLResponse, Data))? { nil }

    override func startLoading() {
        guard let handler = Self.requestHandler else {
            client?.urlProtocol(self, didFailWithError: URLError(.unknown))
            return
        }
        // Normalise body: URLSession converts httpBody → httpBodyStream when routing
        // through a URLProtocol subclass. Read the stream so tests can inspect the body.
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
                    // EOF
                    break
                } else {
                    // Stream error
                    stream.close()
                    let error = stream.streamError ?? URLError(.cannotDecodeRawData)
                    client?.urlProtocol(self, didFailWithError: error)
                    return
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

final class ShareExtensionMock: BaseMockProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var handler: ((URLRequest, Data?) throws -> (HTTPURLResponse, Data))?
    override class var requestHandler: ((URLRequest, Data?) throws -> (HTTPURLResponse, Data))? { handler }
}

func makeSession(for protocolClass: AnyClass) -> URLSession {
    let config = URLSessionConfiguration.ephemeral
    config.protocolClasses = [protocolClass]
    return URLSession(configuration: config)
}
