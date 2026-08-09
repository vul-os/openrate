package org.vulos.openrate;

/**
 * Thrown when openrate cannot be located, started, or made to answer.
 *
 * <p>Both paths use this: the sidecar raises it when the binary is missing or
 * the server never became healthy, and the direct path raises it carrying the
 * shared library's own error string, which is plain UTF-8 text and not JSON.
 */
public class OpenRateException extends RuntimeException {

    public OpenRateException(String message) {
        super(message);
    }

    public OpenRateException(String message, Throwable cause) {
        super(message, cause);
    }
}
